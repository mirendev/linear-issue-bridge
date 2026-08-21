// Package workloadauth verifies Miren workload identity tokens, so the bridge
// can tell that a request came from one of our own apps rather than from the
// internet at large.
//
// This is a trimmed reimplementation of runtime's pkg/oidcauth.Validator, and
// a sibling of the copy in infra's tools/gatehouse. It is duplicated rather
// than imported because pkg/oidcauth is not a leaf: validator.go needs only
// go-jose and golang-jwt, but the package also carries authenticator.go and
// composite.go, which pull in pkg/rpc, pkg/entity, and two API packages. Go
// compiles whole packages, so importing the validator today means importing
// the RPC and entity stack, which is a poor trade for a service whose other
// dependency is a markdown renderer.
//
// The real fix is to pull Validator into a leaf package in runtime that
// pkg/oidcauth then consumes; this file should be deleted the day that lands.
package workloadauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// signingAlgs is the set of asymmetric algorithms we accept. Without it a token
// nominates its own: "none" with no signature, or HMAC using a public key as
// the shared secret.
//
// Deleting this leaves the suite green, because the key-type check in
// fetchJWKS already stops the same attacks. Keep it anyway: the two barriers
// are independent, and this one holds if that one is ever relaxed.
var signingAlgs = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
	"EdDSA",
}

const (
	discoveryTTL = time.Hour
	jwksTTL      = time.Hour
	// Floor between forced key-set refetches. A signature we cannot verify is
	// the shape of a key rotation, but it is also the shape of a forged token,
	// and refetching per request would make an attacker's garbage into our
	// outbound traffic.
	minRefreshInterval = time.Minute
	fetchTimeout       = 10 * time.Second
	maxBodyBytes       = 1 << 20
)

// Claims is the subset of a workload identity token the bridge cares about.
type Claims struct {
	Issuer         string
	Subject        string
	App            string
	ClusterID      string
	OrganizationID string
	Expiry         time.Time
}

// Config describes who we are and whom we trust.
type Config struct {
	// TrustedIssuers must be exact issuer URLs, never patterns. See the note
	// on RequireOrganization for why that matters.
	TrustedIssuers []string
	// Audience is the value callers must mint their tokens for: us.
	Audience string
	// RequireOrganization, when set, additionally requires that the caller's
	// organization_id matches.
	//
	// Trust here is anchored on an exact-match list of issuer URLs. Each issuer
	// belongs to one cluster, which belongs to one organization, so pinning the
	// issuer already pins the organization and this check confirms something
	// already settled. That invariant holds only while the trusted set is exact
	// URLs. The moment it becomes a pattern, a wildcard, or a shared
	// multi-tenant anchor, issuer stops implying organization and this becomes
	// genuinely load-bearing. Cheap to carry now, an audit to add later.
	RequireOrganization string
}

type discovery struct {
	Issuer  string `json:"issuer"`
	JwksURI string `json:"jwks_uri"`
}

type issuerState struct {
	discovery     *discovery
	discoveryTime time.Time
	jwks          *jose.JSONWebKeySet
	jwksTime      time.Time
	lastRefresh   time.Time
}

// Verifier validates tokens from a fixed set of trusted issuers, caching each
// issuer's discovery document and key set.
type Verifier struct {
	cfg Config

	mu      sync.RWMutex
	state   map[string]*issuerState
	trusted map[string]bool
	client  *http.Client
}

func New(cfg Config) (*Verifier, error) {
	if cfg.Audience == "" {
		return nil, errors.New("workloadauth: an audience is required")
	}
	if len(cfg.TrustedIssuers) == 0 {
		return nil, errors.New("workloadauth: at least one trusted issuer is required")
	}
	trusted := make(map[string]bool, len(cfg.TrustedIssuers))
	for _, iss := range cfg.TrustedIssuers {
		iss = strings.TrimRight(strings.TrimSpace(iss), "/")
		if iss == "" {
			continue
		}
		if strings.ContainsAny(iss, "*?") {
			return nil, fmt.Errorf("workloadauth: issuer %q looks like a pattern; trust exact URLs only", iss)
		}
		trusted[iss] = true
	}
	if len(trusted) == 0 {
		return nil, errors.New("workloadauth: at least one trusted issuer is required")
	}
	return &Verifier{
		cfg:     cfg,
		state:   make(map[string]*issuerState),
		trusted: trusted,
		client:  &http.Client{Timeout: fetchTimeout},
	}, nil
}

// Authenticate verifies the request's bearer token. It satisfies the shape the
// roadmap handlers expect.
func (v *Verifier) Authenticate(ctx context.Context, r *http.Request) error {
	token, err := bearerToken(r)
	if err != nil {
		return err
	}
	_, err = v.Verify(ctx, token)
	return err
}

func bearerToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", errors.New("no Authorization header")
	}
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", errors.New("no bearer token in the Authorization header")
	}
	return strings.TrimSpace(token), nil
}

// Verify checks a token against the issuer it names and returns its claims.
//
// The issuer is read from the token before anything is verified, because which
// key set to check the signature against depends on it. That value is
// untrusted: all it does is select one of the issuers we already decided to
// trust, and the signature is then checked against that issuer's published
// keys. A token naming an unknown issuer is refused without any network call.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	issuer, err := peekIssuer(token)
	if err != nil {
		return nil, fmt.Errorf("reading token issuer: %w", err)
	}
	if !v.trusted[strings.TrimRight(issuer, "/")] {
		return nil, fmt.Errorf("issuer %q is not trusted", issuer)
	}

	parsed, err := v.parse(ctx, token, issuer, false)
	// An unknown or stale key is the signature we expect during a key rotation,
	// so refetch the key set once before giving up — but no more often than
	// minRefreshInterval, so a stream of forged tokens cannot turn into a
	// stream of outbound requests.
	if err != nil && (errors.Is(err, jwt.ErrTokenUnverifiable) || errors.Is(err, jwt.ErrTokenSignatureInvalid)) {
		if v.mayRefresh(issuer) {
			parsed, err = v.parse(ctx, token, issuer, true)
		}
	}
	if err != nil {
		return nil, err
	}

	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("token claims are not an object")
	}

	// Checked here rather than via jwt.WithAudience so the error names the
	// audience presented, which is the difference between a five-minute and a
	// fifty-minute debugging session when a caller is misconfigured.
	if !audienceContains(mc, v.cfg.Audience) {
		return nil, fmt.Errorf("token audience %v does not include %q", mc["aud"], v.cfg.Audience)
	}

	c := &Claims{Issuer: issuer}
	c.Subject, _ = mc["sub"].(string)
	c.App, _ = mc["app"].(string)
	c.ClusterID, _ = mc["cluster_id"].(string)
	c.OrganizationID, _ = mc["organization_id"].(string)
	if exp, ok := mc["exp"].(float64); ok {
		c.Expiry = time.Unix(int64(exp), 0)
	}

	if want := v.cfg.RequireOrganization; want != "" && c.OrganizationID != want {
		return nil, fmt.Errorf("token organization %q is not %q", c.OrganizationID, want)
	}
	return c, nil
}

// mayRefresh reports whether enough time has passed to force another key-set
// fetch for this issuer, and records the attempt when it allows one.
func (v *Verifier) mayRefresh(issuer string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.state[issuer]
	if st == nil {
		return true
	}
	if time.Since(st.lastRefresh) < minRefreshInterval {
		return false
	}
	st.lastRefresh = time.Now()
	return true
}

func (v *Verifier) parse(ctx context.Context, token, issuer string, refresh bool) (*jwt.Token, error) {
	keyFunc, err := v.keyFunc(ctx, issuer, refresh)
	if err != nil {
		return nil, fmt.Errorf("resolving keys for %s: %w", issuer, err)
	}

	parser := jwt.NewParser(
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods(signingAlgs),
	)

	parsed, err := parser.Parse(token, keyFunc)
	if err != nil {
		return nil, fmt.Errorf("token rejected: %w", err)
	}
	if !parsed.Valid {
		return nil, errors.New("token is not valid")
	}
	return parsed, nil
}

func (v *Verifier) keyFunc(ctx context.Context, issuer string, refresh bool) (jwt.Keyfunc, error) {
	v.mu.RLock()
	st := v.state[issuer]
	if st != nil && st.jwks != nil && !refresh && time.Since(st.jwksTime) < jwksTTL {
		jwks := st.jwks
		v.mu.RUnlock()
		return keyFuncFor(jwks), nil
	}
	v.mu.RUnlock()

	doc, err := v.getDiscovery(ctx, issuer)
	if err != nil {
		return nil, err
	}
	jwks, err := v.fetchJWKS(ctx, doc.JwksURI)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state[issuer] == nil {
		v.state[issuer] = &issuerState{}
	}
	v.state[issuer].jwks = jwks
	v.state[issuer].jwksTime = time.Now()
	return keyFuncFor(jwks), nil
}

func (v *Verifier) getDiscovery(ctx context.Context, issuer string) (*discovery, error) {
	v.mu.RLock()
	if st := v.state[issuer]; st != nil && st.discovery != nil && time.Since(st.discoveryTime) < discoveryTTL {
		doc := st.discovery
		v.mu.RUnlock()
		return doc, nil
	}
	v.mu.RUnlock()

	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	var doc discovery
	if err := v.getJSON(ctx, url, &doc); err != nil {
		return nil, fmt.Errorf("fetching discovery document: %w", err)
	}

	// The document has to agree about who it belongs to, or a trusted issuer
	// URL could be pointed at someone else's keys.
	if strings.TrimRight(doc.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return nil, fmt.Errorf("discovery document claims issuer %q, expected %q", doc.Issuer, issuer)
	}
	if doc.JwksURI == "" {
		return nil, errors.New("discovery document has no jwks_uri")
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state[issuer] == nil {
		v.state[issuer] = &issuerState{}
	}
	v.state[issuer].discovery = &doc
	v.state[issuer].discoveryTime = time.Now()
	return &doc, nil
}

func (v *Verifier) fetchJWKS(ctx context.Context, uri string) (*jose.JSONWebKeySet, error) {
	var jwks jose.JSONWebKeySet
	if err := v.getJSON(ctx, uri, &jwks); err != nil {
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	if len(jwks.Keys) == 0 {
		return nil, errors.New("JWKS contains no keys")
	}
	// Second, independent barrier against a symmetric key being used as a
	// signing secret. A key published in a JWKS is public by definition, so a
	// symmetric one would let anyone mint a valid HMAC signature that passes
	// every other check. signingAlgs already refuses to run HMAC at all; this
	// makes the same guarantee structural rather than one line somebody could
	// delete, by refusing the key on arrival. A Miren issuer only ever
	// publishes asymmetric public keys, so anything else is malformed.
	for _, k := range jwks.Keys {
		switch k.Key.(type) {
		case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
		default:
			return nil, fmt.Errorf("JWKS key %q is %T, not an asymmetric public key", k.KeyID, k.Key)
		}
	}
	return &jwks, nil
}

func (v *Verifier) getJSON(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned status %d", url, resp.StatusCode)
	}
	// Bounded so a hostile or broken endpoint cannot make us read forever.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

func keyFuncFor(jwks *jose.JSONWebKeySet) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			// Miren's issuer stamps a kid on everything it signs, so a token
			// without one did not come from a cluster we trust. Refusing beats
			// guessing which key to try.
			return nil, errors.New("token has no kid header")
		}
		keys := jwks.Key(kid)
		if len(keys) == 0 {
			return nil, fmt.Errorf("no key %q in JWKS: %w", kid, jwt.ErrTokenUnverifiable)
		}
		return keys[0].Key, nil
	}
}

func audienceContains(mc jwt.MapClaims, expected string) bool {
	switch aud := mc["aud"].(type) {
	case string:
		return aud == expected
	case []any:
		for _, a := range aud {
			if s, ok := a.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}

// peekIssuer reads the iss claim without verifying anything. The value only
// selects a key set; it is worthless until that key set has verified the token.
func peekIssuer(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding claims: %w", err)
	}
	var c struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", fmt.Errorf("parsing claims: %w", err)
	}
	if c.Issuer == "" {
		return "", errors.New("token has no issuer claim")
	}
	return c.Issuer, nil
}
