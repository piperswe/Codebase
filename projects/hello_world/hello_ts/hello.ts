import { Vector2 } from "$/lib/ts/vector/vector.js";

console.log("Hello, world!");
const a = new Vector2(1, 2);
const b = new Vector2(2, 54);
console.log(`${a.toString()} + ${b.toString()} = ${a.add(b).toString()}`);
console.log(`${a.toString()} is ${a.angleDegrees(b)}deg to ${b.toString()}`);
