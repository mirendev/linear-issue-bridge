package workloadauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

const testKID = "test-key-1"

// fakeIssuer is a cluster: a discovery document, a JWKS, and a signing key.
type fakeIssuer struct {
	// The JWKS handler runs on the server's goroutine while the rotation test
	// swaps the key from the test's, so the mutable fields need a lock.
	mu     sync.Mutex
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
	// issuerOverride makes the discovery document claim a different issuer,
	// for the "points at someone else's keys" case.
	issuerOverride string
	jwksCalls      int
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	f := &fakeIssuer{key: key, kid: testKID}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss := f.issuer
		if f.issuerOverride != "" {
			iss = f.issuerOverride
		}
		_ = json.NewEncoder(w).Encode(discovery{Issuer: iss, JwksURI: f.issuer + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.jwksCalls++
		key, kid := f.key.Public(), f.kid
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key, KeyID: kid, Algorithm: "RS256", Use: "sig",
		}}})
	})

	f.srv = httptest.NewServer(mux)
	f.issuer = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) rotate(t *testing.T, kid string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	f.mu.Lock()
	f.key, f.kid = key, kid
	f.mu.Unlock()
}

func (f *fakeIssuer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jwksCalls
}

func (f *fakeIssuer) token(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	f.mu.Lock()
	key, kid, issuer := f.key, f.kid, f.issuer
	f.mu.Unlock()
	claims := jwt.MapClaims{
		"iss":             issuer,
		"sub":             "org:miren:app:mirendev:sandbox:sbx_1",
		"aud":             "https://bridge.example",
		"app":             "mirendev",
		"cluster_id":      "prod",
		"organization_id": "miren",
		"iat":             time.Now().Unix(),
		"exp":             time.Now().Add(time.Hour).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func newVerifier(t *testing.T, f *fakeIssuer, org string) *Verifier {
	t.Helper()
	v, err := New(Config{
		TrustedIssuers:      []string{f.issuer},
		Audience:            "https://bridge.example",
		RequireOrganization: org,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func TestVerifyAcceptsAGoodToken(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	claims, err := v.Verify(context.Background(), f.token(t, nil))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.App != "mirendev" || claims.OrganizationID != "miren" {
		t.Errorf("claims = %+v", claims)
	}
}

// The classic confusion attack: nominate HMAC and hand over a signature made
// with the issuer's public key, which the attacker also has.
//
// Note what actually rejects this: golang-jwt refuses to use an *rsa.PublicKey
// as an HMAC secret, so the key type stops it before the allowlist is reached.
func TestVerifyRejectsAlgorithmConfusion(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	pubDER, err := jose.JSONWebKey{Key: f.key.Public()}.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": f.issuer, "aud": "https://bridge.example",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = f.kid
	forged, err := tok.SignedString(pubDER)
	if err != nil {
		t.Fatalf("sign forged: %v", err)
	}

	if _, err := v.Verify(context.Background(), forged); err == nil {
		t.Fatal("an HMAC-signed token was accepted")
	}
}

// "alg": "none" must never verify.
func TestVerifyRejectsUnsignedToken(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": f.issuer, "aud": "https://bridge.example",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = f.kid
	unsigned, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}

	if _, err := v.Verify(context.Background(), unsigned); err == nil {
		t.Fatal("an unsigned token was accepted")
	}
}

// A token from a cluster we never named must be refused without a network call.
func TestVerifyRejectsUntrustedIssuer(t *testing.T) {
	trusted := newFakeIssuer(t)
	stranger := newFakeIssuer(t)
	v := newVerifier(t, trusted, "")

	before := stranger.jwksCalls
	if _, err := v.Verify(context.Background(), stranger.token(t, nil)); err == nil {
		t.Fatal("a token from an untrusted issuer was accepted")
	}
	if stranger.jwksCalls != before {
		t.Error("an untrusted issuer's key set was fetched")
	}
}

// A token minted for somebody else must not work on us, even though it is
// genuinely signed by a cluster we trust.
func TestVerifyRejectsWrongAudience(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	tok := f.token(t, func(c jwt.MapClaims) { c["aud"] = "https://someone-else.example" })
	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("a token minted for another audience was accepted")
	}
	if !strings.Contains(err.Error(), "someone-else.example") {
		t.Errorf("error %q should name the audience presented", err)
	}
}

func TestVerifyAcceptsAudienceInAList(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	tok := f.token(t, func(c jwt.MapClaims) {
		c["aud"] = []any{"https://other.example", "https://bridge.example"}
	})
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	tok := f.token(t, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

// Expiry is mandatory: a token that never expires is a bearer credential.
func TestVerifyRejectsTokenWithoutExpiry(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	tok := f.token(t, func(c jwt.MapClaims) { delete(c, "exp") })
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("a token with no expiry was accepted")
	}
}

func TestVerifyRejectsTokenWithoutKID(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	claims := jwt.MapClaims{
		"iss": f.issuer, "aud": "https://bridge.example",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(f.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := v.Verify(context.Background(), signed); err == nil {
		t.Fatal("a token with no kid was accepted")
	}
}

// A trusted issuer URL must not be usable to serve somebody else's keys.
func TestVerifyRejectsDiscoveryIssuerMismatch(t *testing.T) {
	f := newFakeIssuer(t)
	f.issuerOverride = "https://evil.example"
	v := newVerifier(t, f, "")

	if _, err := v.Verify(context.Background(), f.token(t, nil)); err == nil {
		t.Fatal("a discovery document claiming another issuer was accepted")
	}
}

// Defence in depth: enforced only when configured.
func TestRequireOrganization(t *testing.T) {
	f := newFakeIssuer(t)

	if _, err := newVerifier(t, f, "miren").Verify(context.Background(), f.token(t, nil)); err != nil {
		t.Errorf("matching organization was rejected: %v", err)
	}

	v := newVerifier(t, f, "someone-else")
	if _, err := v.Verify(context.Background(), f.token(t, nil)); err == nil {
		t.Error("a token from another organization was accepted")
	}

	// Unset means unenforced, which is the default.
	if _, err := newVerifier(t, f, "").Verify(context.Background(),
		f.token(t, func(c jwt.MapClaims) { delete(c, "organization_id") })); err != nil {
		t.Errorf("organization check ran while unconfigured: %v", err)
	}
}

// A rotated signing key must recover without a restart.
func TestVerifyRefetchesKeysOnRotation(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	if _, err := v.Verify(context.Background(), f.token(t, nil)); err != nil {
		t.Fatalf("warm: %v", err)
	}
	callsAfterWarm := f.calls()

	f.rotate(t, "test-key-2")

	if _, err := v.Verify(context.Background(), f.token(t, nil)); err != nil {
		t.Fatalf("Verify after rotation: %v", err)
	}
	if f.calls() <= callsAfterWarm {
		t.Error("the key set was not refetched after rotation")
	}
}

// A pattern in the trusted set breaks the invariant that issuer implies
// organization, so it is refused at construction rather than at request time.
func TestNewRejectsPatternIssuers(t *testing.T) {
	if _, err := New(Config{
		TrustedIssuers: []string{"https://*.miren.systems"},
		Audience:       "https://bridge.example",
	}); err == nil {
		t.Error("a wildcard issuer was accepted")
	}
}

func TestNewRequiresAudienceAndIssuers(t *testing.T) {
	if _, err := New(Config{TrustedIssuers: []string{"https://a.example"}}); err == nil {
		t.Error("a verifier with no audience was accepted")
	}
	if _, err := New(Config{Audience: "https://bridge.example"}); err == nil {
		t.Error("a verifier with no trusted issuers was accepted")
	}
}

func TestAuthenticateReadsBearerToken(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	req := httptest.NewRequest(http.MethodPost, "/api/roadmap/vote", nil)
	req.Header.Set("Authorization", "Bearer "+f.token(t, nil))
	if err := v.Authenticate(context.Background(), req); err != nil {
		t.Errorf("Authenticate: %v", err)
	}

	for _, tc := range []struct{ name, header string }{
		{"missing", ""},
		{"not bearer", "Basic abc"},
		{"empty bearer", "Bearer "},
		{"garbage", "Bearer not-a-jwt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/roadmap/vote", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if err := v.Authenticate(context.Background(), r); err == nil {
				t.Error("accepted a request without a valid bearer token")
			}
		})
	}
}

// A symmetric key published in a JWKS is public by definition, so anyone can
// mint a valid HS256 signature with it. Two independent barriers refuse it:
// fetchJWKS rejects the key set on arrival, and the algorithm allowlist refuses
// HMAC at the parser. The fetch-time check is what fires here, since it runs
// first; remove it and the allowlist still catches this, which is the point of
// having both. TestVerifyRejectsSymmetricKeyAtFetchTime pins the first one.
func TestVerifyRejectsSymmetricKeyForgery(t *testing.T) {
	secret := []byte("a symmetric key that should never be in a JWKS")
	const symKID = "sym-1"

	mux := http.NewServeMux()
	var issuer string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(discovery{Issuer: issuer, JwksURI: issuer + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: secret, KeyID: symKID, Algorithm: "HS256", Use: "sig",
		}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	issuer = srv.URL

	v, err := New(Config{TrustedIssuers: []string{issuer}, Audience: "https://bridge.example"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": issuer, "aud": "https://bridge.example",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = symKID
	forged, err := tok.SignedString(secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := v.Verify(context.Background(), forged); err == nil {
		t.Fatal("a token signed with a published symmetric key was accepted")
	}
}

// A stream of unverifiable tokens must not turn into a stream of outbound JWKS
// fetches. The first failure is allowed one refetch; the rest ride the floor.
func TestVerifyBoundsRefetchOnBadSignatures(t *testing.T) {
	f := newFakeIssuer(t)
	v := newVerifier(t, f, "")

	if _, err := v.Verify(context.Background(), f.token(t, nil)); err != nil {
		t.Fatalf("warm: %v", err)
	}
	// Forge a signature the cached key set cannot verify.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": f.issuer, "aud": "https://bridge.example",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = testKID
	forged, err := tok.SignedString(other)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	before := f.calls()
	for range 10 {
		if _, err := v.Verify(context.Background(), forged); err == nil {
			t.Fatal("a forged signature verified")
		}
	}
	if got := f.calls() - before; got > 1 {
		t.Errorf("%d key-set fetches for 10 bad signatures, want at most 1", got)
	}
}
