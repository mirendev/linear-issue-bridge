// Fills the issue shell in from /api/issue once Linear answers.
(function() {
  var main = document.querySelector('main[data-issue-shell]');
  var id = main.getAttribute('data-issue-shell');
  loadFromLinear('/api/issue/' + encodeURIComponent(id), main.querySelector('.linear-status'), function(data) {
    main.innerHTML = data.html;
    document.title = data.title;
  });
})();
