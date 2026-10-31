function main(input) {
  const entity = db.entity(input.entity);
  const page = entity.refs("device/link", {limit: 20});
  return {id: entity.id, connections: page.items.map(e => ({id: e.id, kind: e.get("entity/kind")})), complete: page.complete, nextCursor: page.nextCursor};
}
