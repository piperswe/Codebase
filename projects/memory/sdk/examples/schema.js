function main(input) { return db.schema.search({query: input.concept, limit: 10}); }
