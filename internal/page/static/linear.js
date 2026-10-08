// loadFromLinear fetches a JSON endpoint that is backed by Linear, narrating
// progress in a linear-status element (see the "linear-status" partial).
// Failures retry on a backoff, with a button to skip the wait; once the
// backoff is spent, the button is the only way forward.
(function() {
  var SLOW_AFTER_MS = 3000;
  var REQUEST_TIMEOUT_MS = 20000;
  var RETRY_DELAYS_S = [2, 4, 8];

  window.loadFromLinear = function(url, statusEl, onData) {
    var text = statusEl.querySelector('.linear-status-text');
    var retryBtn = statusEl.querySelector('.linear-status-retry');
    var failures = 0;
    var timers = [];

    function stopTimers() {
      timers.forEach(function(t) { clearTimeout(t); clearInterval(t); });
      timers = [];
    }

    function show(state, message) {
      statusEl.hidden = false;
      statusEl.setAttribute('data-state', state);
      text.textContent = message;
    }

    function attempt() {
      stopTimers();
      retryBtn.hidden = true;
      show('loading', failures ? 'Trying Linear again…' : 'Talking to Linear…');
      timers.push(setTimeout(function() {
        show('slow', 'Linear’s being slow, still trying…');
      }, SLOW_AFTER_MS));

      var opts = { headers: { 'Accept': 'application/json' } };
      if (window.AbortController) {
        var ctrl = new AbortController();
        opts.signal = ctrl.signal;
        timers.push(setTimeout(function() { ctrl.abort(); }, REQUEST_TIMEOUT_MS));
      }

      fetch(url, opts)
        .then(function(res) {
          if (!res.ok) throw new Error('HTTP ' + res.status);
          return res.json();
        })
        .then(function(data) {
          stopTimers();
          statusEl.setAttribute('data-state', 'ready');
          statusEl.hidden = true;
          onData(data);
        })
        .catch(function(err) {
          console.error('loading from Linear failed', url, err);
          stopTimers();
          failed();
        });
    }

    function failed() {
      failures++;
      retryBtn.hidden = false;
      if (failures > RETRY_DELAYS_S.length) {
        show('error', 'Linear isn’t answering right now.');
        return;
      }
      var left = RETRY_DELAYS_S[failures - 1];
      var tick = function() {
        show('retrying', 'Linear didn’t answer. Retrying in ' + left + 's');
      };
      tick();
      timers.push(setInterval(function() {
        left--;
        if (left <= 0) attempt(); else tick();
      }, 1000));
    }

    retryBtn.addEventListener('click', function() {
      // A manual retry after giving up earns a fresh round of backoff.
      if (statusEl.getAttribute('data-state') === 'error') failures = 0;
      attempt();
    });

    attempt();
  };
})();
