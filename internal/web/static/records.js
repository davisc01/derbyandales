// The records page: finding one person in a table of everybody's best.

(function () {
  "use strict";

  const search = document.getElementById("pb-search");
  const table = document.getElementById("pb-table");
  if (!search || !table) return;

  search.addEventListener("input", function () {
    const q = search.value.trim().toLowerCase();
    table.querySelectorAll("tr[data-search]").forEach(function (row) {
      row.hidden = q !== "" && row.dataset.search.indexOf(q) === -1;
    });
  });
})();
