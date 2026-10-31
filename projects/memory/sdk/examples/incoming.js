function main(input) { const p = db.entity(input.entity).incoming("device/link", {limit: 20}); return {items: p.items.map(e => e.id), complete: p.complete, nextCursor: p.nextCursor}; }
