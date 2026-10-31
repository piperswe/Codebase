function main(input) { return db.entities(input.entities).map(e => e.pull({select: {"entity/kind": true}})); }
