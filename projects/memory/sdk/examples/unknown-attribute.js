function main(input) { try { db.entity(input.entity).get("unknown/name"); } catch (error) { return db.schema.search({query: "year", limit: 10}); } throw Error("unknown attribute was accepted"); }
