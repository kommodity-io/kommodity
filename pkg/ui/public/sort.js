// Machine table sort helpers. Pure comparator extracted so the logic can be
// unit tested without a DOM (see sort.test.js).
(function () {
    "use strict";

    // Index of the Age column in machine detail tables. Keep in sync with
    // cluster_detail.html: Health, Phase, Name, NodeName, Zone,
    // Kubernetes Version, Age.
    const ageColumnIndex = 6;

    function creationTimeOf(row) {
        const cell = row.cells[ageColumnIndex];
        return cell ? (cell.getAttribute("data-creation-time") || "") : "";
    }

    // Sort machine rows by creation time.
    // dir: "desc" = newest first, "asc" = oldest first.
    // Mutates rows in place like Array.prototype.sort and returns the array.
    function sortMachinesByCreation(rows, dir) {
        return rows.sort(function (a, b) {
            const timeA = creationTimeOf(a);
            const timeB = creationTimeOf(b);
            if (dir === "desc") {
                return timeB.localeCompare(timeA);
            }
            return timeA.localeCompare(timeB);
        });
    }

    // Indicator glyph for a sort direction.
    function sortIndicatorFor(dir) {
        return dir === "desc" ? "↓" : "↑";
    }

    if (typeof window !== "undefined") {
        window.sortMachinesByCreation = sortMachinesByCreation;
        window.sortIndicatorFor = sortIndicatorFor;
        window.ageColumnIndex = ageColumnIndex;
    }
    if (typeof module !== "undefined" && module.exports) {
        module.exports = {
            sortMachinesByCreation,
            sortIndicatorFor,
            ageColumnIndex,
        };
    }
})();
