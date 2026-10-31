function main(input) { const year = db.entity(input.entity).get("device/year"); return {exact: year, number: db.toSafeNumber(year)}; }
