import { Vector2 } from './vector.js';
import { test, expect } from 'vitest';

test('Vector2 addition', () => {
    const v1 = new Vector2(1, 2);
    const v2 = new Vector2(3, 4);
    const result = v1.add(v2);
    expect(result.x).toBe(4);
    expect(result.y).toBe(6);
});