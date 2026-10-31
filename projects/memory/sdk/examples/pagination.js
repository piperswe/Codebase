function main(input) { const first = db.search({kind: "attribute", limit: 2}); return {first, second: first.nextCursor ? db.search({kind: "attribute", limit: 2, cursor: first.nextCursor}) : null}; }
