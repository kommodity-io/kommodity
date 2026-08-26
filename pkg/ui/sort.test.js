"use strict";

const assert = require("assert");
const path = require("path");
const { sortMachinesByCreation, sortIndicatorFor, ageColumnIndex } =
    require(path.join(__dirname, "public", "sort.js"));

// Build a fake row whose Age cell carries a data-creation-time attribute.
// Mirrors the real <tr> shape used by cluster_detail.html.
function makeRow(creationTime) {
    const cells = [];
    for (let i = 0; i < ageColumnIndex + 1; i++) {
        cells.push({
            attr: {},
            getAttribute(name) {
                return this.attr[name] || null;
            },
        });
    }
    cells[ageColumnIndex].attr["data-creation-time"] = creationTime;
    return { cells };
}

function times(rows) {
    return rows.map(function (r) {
        return r.cells[ageColumnIndex].attr["data-creation-time"];
    });
}

// Given unsorted rows, desc puts newest (largest RFC3339 string) first.
(function descNewestFirst() {
    const rows = [
        makeRow("2024-01-01T00:00:00Z"),
        makeRow("2024-03-01T00:00:00Z"),
        makeRow("2024-02-01T00:00:00Z"),
    ];
    const sorted = sortMachinesByCreation(rows.slice(), "desc");
    assert.deepStrictEqual(times(sorted), [
        "2024-03-01T00:00:00Z",
        "2024-02-01T00:00:00Z",
        "2024-01-01T00:00:00Z",
    ]);
})();

// Ascending keeps oldest first.
(function ascOldestFirst() {
    const rows = [
        makeRow("2024-03-01T00:00:00Z"),
        makeRow("2024-01-01T00:00:00Z"),
        makeRow("2024-02-01T00:00:00Z"),
    ];
    const sorted = sortMachinesByCreation(rows.slice(), "asc");
    assert.deepStrictEqual(times(sorted), [
        "2024-01-01T00:00:00Z",
        "2024-02-01T00:00:00Z",
        "2024-03-01T00:00:00Z",
    ]);
})();

// Missing creation time sorts to the end regardless of direction.
(function missingCreationTimeDegradesGracefully() {
    const rows = [
        makeRow("2024-02-01T00:00:00Z"),
        makeRow(""),
        makeRow("2024-01-01T00:00:00Z"),
    ];
    const desc = sortMachinesByCreation(rows.slice(), "desc");
    assert.deepStrictEqual(times(desc), [
        "2024-02-01T00:00:00Z",
        "2024-01-01T00:00:00Z",
        "",
    ]);
    const asc = sortMachinesByCreation(rows.slice(), "asc");
    assert.deepStrictEqual(times(asc), [
        "",
        "2024-01-01T00:00:00Z",
        "2024-02-01T00:00:00Z",
    ]);
})();

// Indicator glyphs match direction.
assert.strictEqual(sortIndicatorFor("desc"), "↓");
assert.strictEqual(sortIndicatorFor("asc"), "↑");

console.log("pkg/ui/sort.test.js: all tests passed");
