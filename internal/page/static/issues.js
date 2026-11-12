// Renders the /issues list from /api/issues, with status filters.
(function() {
  var root = document.querySelector('.issues-list');
  var skeleton = root.querySelector('.issues-skeleton');
  var list = root.querySelector('.issues-data');
  var filters = root.querySelector('.issues-filters');
  var count = root.querySelector('.issues-count');
  var empty = root.querySelector('.issues-empty');

  var currentFilter = 'all';
  var allItems = [];

  function setStatusPill(span, status, color) {
    span.className = 'status';
    span.style.color = color;
    span.style.background = color + '1a';
    span.textContent = status;
  }

  function buildItem(issue) {
    var li = document.createElement('li');
    li.className = 'issues-item';
    li.setAttribute('data-status', issue.status);

    var a = document.createElement('a');
    a.className = 'issues-item-link';
    a.href = '/' + issue.identifier;

    var header = document.createElement('div');
    header.className = 'issues-item-header';
    var id = document.createElement('span');
    id.className = 'issues-item-id';
    id.textContent = issue.identifier;
    var status = document.createElement('span');
    setStatusPill(status, issue.status, issue.statusColor);
    header.appendChild(id);
    header.appendChild(status);

    var title = document.createElement('span');
    title.className = 'issues-item-title';
    title.textContent = issue.title;

    var time = document.createElement('time');
    time.className = 'issues-item-date';
    time.setAttribute('datetime', issue.updatedAtIso);
    time.textContent = 'Updated ' + issue.updatedDisplay;

    a.appendChild(header);
    a.appendChild(title);
    a.appendChild(time);
    li.appendChild(a);
    return li;
  }

  function applyFilter(filter) {
    currentFilter = filter;
    var visible = 0;
    allItems.forEach(function(item) {
      var status = item.getAttribute('data-status');
      var show = (filter === 'all') ? status !== 'Done' : status === filter;
      item.style.display = show ? '' : 'none';
      if (show) visible++;
    });
    if (visible === 0 && allItems.length > 0) {
      empty.textContent = 'No issues match this filter.';
      empty.hidden = false;
    } else {
      empty.hidden = true;
    }
    var noun = visible === 1 ? 'issue' : 'issues';
    var label = filter === 'all' ? 'open public' : filter;
    count.textContent = visible + ' ' + label + ' ' + noun;
  }

  function renderFilters(statuses) {
    filters.innerHTML = '';
    var allBtn = document.createElement('button');
    allBtn.className = 'issues-filter active';
    allBtn.setAttribute('data-filter', 'all');
    allBtn.textContent = 'All';
    filters.appendChild(allBtn);
    statuses.forEach(function(s) {
      var btn = document.createElement('button');
      btn.className = 'issues-filter';
      btn.setAttribute('data-filter', s);
      btn.textContent = s;
      filters.appendChild(btn);
    });
    filters.querySelectorAll('.issues-filter').forEach(function(btn) {
      btn.addEventListener('click', function() {
        filters.querySelectorAll('.issues-filter').forEach(function(b) { b.classList.remove('active'); });
        btn.classList.add('active');
        applyFilter(btn.getAttribute('data-filter'));
      });
    });
    filters.hidden = statuses.length === 0;
  }

  function render(data) {
    list.innerHTML = '';
    allItems = data.issues.map(buildItem);
    allItems.forEach(function(item) { list.appendChild(item); });
    renderFilters(data.statuses);

    skeleton.remove();
    list.hidden = false;
    root.setAttribute('data-state', 'ready');

    if (allItems.length === 0) {
      empty.textContent = 'No open public issues right now.';
      empty.hidden = false;
      count.textContent = '';
      return;
    }
    applyFilter('all');
  }

  loadFromLinear('/api/issues', root.querySelector('.linear-status'), render);
})();
