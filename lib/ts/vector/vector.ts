export interface Vector<T = unknown> {
  toString(): string;
  add(other: T): T;
  subtract(other: T): T;
  multiply(scalar: number): T;
  divide(scalar: number): T;
  magnitude(): number;
  normalize(): T;
  dot(other: T): number;
  angle(other: T): number;
  angleRadians(other: T): number;
  angleDegrees(other: T): number;
}

export class Vector2 implements Vector<Vector2> {
  #x: number;
  #y: number;

  static NULL = new Vector2(0, 0);

  constructor(x: number, y: number) {
    this.#x = x;
    this.#y = y;
  }

  get x(): number {
    return this.#x;
  }

  get y(): number {
    return this.#y;
  }

  toString(): string {
    return `(${this.x}, ${this.y})`;
  }

  add(other: Vector2): Vector2 {
    return new Vector2(this.x + other.x, this.y + other.y);
  }

  subtract(other: Vector2): Vector2 {
    return new Vector2(this.x - other.x, this.y - other.y);
  }

  multiply(scalar: number): Vector2 {
    return new Vector2(this.x * scalar, this.y * scalar);
  }

  divide(scalar: number): Vector2 {
    return new Vector2(this.x / scalar, this.y / scalar);
  }

  magnitude(): number {
    return Math.sqrt(this.x * this.x + this.y * this.y);
  }

  normalize(): Vector2 {
    const mag = this.magnitude();
    return mag === 0 ? new Vector2(0, 0) : this.divide(mag);
  }

  dot(other: Vector2): number {
    return this.x * other.x + this.y * other.y;
  }

  angle(other: Vector2): number {
    const dot = this.dot(other);
    const mags = this.magnitude() * other.magnitude();
    return mags === 0 ? 0 : Math.acos(dot / mags);
  }

  angleRadians(other: Vector2): number {
    return this.angle(other);
  }

  angleDegrees(other: Vector2): number {
    return this.angle(other) * (180 / Math.PI);
  }
}

export class Vector3 implements Vector<Vector3> {
  #x: number;
  #y: number;
  #z: number;

  constructor(x: number, y: number, z: number) {
    this.#x = x;
    this.#y = y;
    this.#z = z;
  }

  get x() {
    return this.#x;
  }

  get y() {
    return this.#y;
  }

  get z() {
    return this.#z;
  }

  toString(): string {
    return `(${this.x}, ${this.y}, ${this.z})`;
  }

  add(other: Vector3): Vector3 {
    return new Vector3(this.x + other.x, this.y + other.y, this.z + other.z);
  }

  subtract(other: Vector3): Vector3 {
    return new Vector3(this.x - other.x, this.y - other.y, this.z - other.z);
  }

  multiply(scalar: number): Vector3 {
    return new Vector3(this.x * scalar, this.y * scalar, this.z * scalar);
  }

  divide(scalar: number): Vector3 {
    return new Vector3(this.x / scalar, this.y / scalar, this.z / scalar);
  }

  magnitude(): number {
    return Math.sqrt(this.x * this.x + this.y * this.y + this.z * this.z);
  }

  normalize(): Vector3 {
    const mag = this.magnitude();
    return mag === 0 ? new Vector3(0, 0, 0) : this.divide(mag);
  }

  dot(other: Vector3): number {
    return this.x * other.x + this.y * other.y + this.z * other.z;
  }

  cross(other: Vector3): Vector3 {
    return new Vector3(
      this.y * other.z - this.z * other.y,
      this.z * other.x - this.x * other.z,
      this.x * other.y - this.y * other.x,
    );
  }

  angle(other: Vector3): number {
    const dot = this.dot(other);
    const mags = this.magnitude() * other.magnitude();
    return mags === 0 ? 0 : Math.acos(dot / mags);
  }

  angleRadians(other: Vector3): number {
    return this.angle(other);
  }

  angleDegrees(other: Vector3): number {
    return this.angle(other) * (180 / Math.PI);
  }
}
