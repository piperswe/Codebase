import { Vector2, Vector3 } from "./vector.js";
import { describe, it, expect } from "vitest";

describe("Vector2", () => {
  describe("#x", () => {
    it("should return the correct x value", () => {
      const v = new Vector2(1, 2);
      expect(v.x).toBe(1);
    });
  });
  describe("#y", () => {
    it("should return the correct y value", () => {
      const v = new Vector2(1, 2);
      expect(v.y).toBe(2);
    });
  });
  describe("#toString", () => {
    it("should return the correct string representation", () => {
      const v = new Vector2(1, 2);
      expect(v.toString()).toBe("(1, 2)");
    });
  });
  describe("#add", () => {
    it("should return the correct result of addition", () => {
      const v1 = new Vector2(1, 2);
      const v2 = new Vector2(3, 4);
      const result = v1.add(v2);
      expect(result.x).toBe(4);
      expect(result.y).toBe(6);
    });
  });
  describe("#subtract", () => {
    it("should return the correct result of subtraction", () => {
      const v1 = new Vector2(5, 7);
      const v2 = new Vector2(3, 4);
      const result = v1.subtract(v2);
      expect(result.x).toBe(2);
      expect(result.y).toBe(3);
    });
  });
  describe("#multiply", () => {
    it("should return the correct result of multiplication", () => {
      const v = new Vector2(2, 3);
      const result = v.multiply(2);
      expect(result.x).toBe(4);
      expect(result.y).toBe(6);
    });
  });
  describe("#divide", () => {
    it("should return the correct result of division", () => {
      const v = new Vector2(4, 6);
      const result = v.divide(2);
      expect(result.x).toBe(2);
      expect(result.y).toBe(3);
    });
  });
  describe("#magnitude", () => {
    it("should return the correct magnitude", () => {
      const v = new Vector2(3, 4);
      expect(v.magnitude()).toBe(5);
    });
  });
  describe("#normalize", () => {
    it("should return the correct normalized vector", () => {
      const v = new Vector2(3, 4);
      const result = v.normalize();
      expect(result.x).toBeCloseTo(3 / 5);
      expect(result.y).toBeCloseTo(4 / 5);
    });
  });
  describe("#dot", () => {
    it("should return the correct dot product", () => {
      const v1 = new Vector2(1, 2);
      const v2 = new Vector2(3, 4);
      const result = v1.dot(v2);
      expect(result).toBe(11);
    });
  });
  describe("#angle", () => {
    it("should return the correct angle between two vectors", () => {
      const v1 = new Vector2(1, 0);
      const v2 = new Vector2(0, 1);
      const result = v1.angle(v2);
      expect(result).toBeCloseTo(Math.PI / 2);
    });
    it("should treat the null vector's angle as zero", () => {
      const v1 = new Vector2(0, 0);
      const v2 = new Vector2(0, 0);
      const result = v1.angle(v2);
      expect(result).toBe(0);
    });
  });
  describe("#angleRadians", () => {
    it("should return the correct angle in radians between two vectors", () => {
      const v1 = new Vector2(1, 0);
      const v2 = new Vector2(0, 1);
      const result = v1.angleRadians(v2);
      expect(result).toBeCloseTo(Math.PI / 2);
    });
  });
  describe("#angleDegrees", () => {
    it("should return the correct angle in degrees between two vectors", () => {
      const v1 = new Vector2(1, 0);
      const v2 = new Vector2(0, 1);
      const result = v1.angleDegrees(v2);
      expect(result).toBeCloseTo(90);
    });
  });
});

describe("Vector3", () => {
  describe("#x", () => {
    it("should return the correct x value", () => {
      const v = new Vector3(1, 2, 3);
      expect(v.x).toBe(1);
    });
  });
  describe("#y", () => {
    it("should return the correct y value", () => {
      const v = new Vector3(1, 2, 3);
      expect(v.y).toBe(2);
    });
  });
  describe("#toString", () => {
    it("should return the correct string representation", () => {
      const v = new Vector3(1, 2, 3);
      expect(v.toString()).toBe("(1, 2, 3)");
    });
  });
  describe("#add", () => {
    it("should return the correct result of addition", () => {
      const v1 = new Vector3(1, 2, 3);
      const v2 = new Vector3(3, 4, 5);
      const result = v1.add(v2);
      expect(result.x).toBe(4);
      expect(result.y).toBe(6);
      expect(result.z).toBe(8);
    });
  });
  describe("#subtract", () => {
    it("should return the correct result of subtraction", () => {
      const v1 = new Vector3(5, 7, 9);
      const v2 = new Vector3(3, 4, 5);
      const result = v1.subtract(v2);
      expect(result.x).toBe(2);
      expect(result.y).toBe(3);
      expect(result.z).toBe(4);
    });
  });
  describe("#multiply", () => {
    it("should return the correct result of multiplication", () => {
      const v = new Vector3(2, 3, 4);
      const result = v.multiply(2);
      expect(result.x).toBe(4);
      expect(result.y).toBe(6);
      expect(result.z).toBe(8);
    });
  });
  describe("#divide", () => {
    it("should return the correct result of division", () => {
      const v = new Vector3(4, 6, 8);
      const result = v.divide(2);
      expect(result.x).toBe(2);
      expect(result.y).toBe(3);
      expect(result.z).toBe(4);
    });
  });
  describe("#magnitude", () => {
    it("should return the correct magnitude", () => {
      const v = new Vector3(3, 4, 12);
      expect(v.magnitude()).toBe(13);
    });
  });
  describe("#normalize", () => {
    it("should return the correct normalized vector", () => {
      const v = new Vector3(3, 4, 12);
      const result = v.normalize();
      expect(result.x).toBeCloseTo(3 / 13);
      expect(result.y).toBeCloseTo(4 / 13);
      expect(result.z).toBeCloseTo(12 / 13);
    });
  });
  describe("#dot", () => {
    it("should return the correct dot product", () => {
      const v1 = new Vector3(1, 2, 3);
      const v2 = new Vector3(3, 4, 5);
      const result = v1.dot(v2);
      expect(result).toBe(26);
    });
  });
  describe("#cross", () => {
    it("should return the correct cross product", () => {
      const v1 = new Vector3(1, 2, 3);
      const v2 = new Vector3(4, 5, 6);
      const result = v1.cross(v2);
      expect(result.x).toBe(-3);
      expect(result.y).toBe(6);
      expect(result.z).toBe(-3);
    });
  });
  describe("#angle", () => {
    it("should return the correct angle between two vectors", () => {
      const v1 = new Vector3(1, 0, 0);
      const v2 = new Vector3(0, 1, 0);
      const result = v1.angle(v2);
      expect(result).toBeCloseTo(Math.PI / 2);
    });
    it("should treat the null vector's angle as zero", () => {
      const v1 = new Vector3(0, 0, 0);
      const v2 = new Vector3(0, 0, 0);
      const result = v1.angle(v2);
      expect(result).toBe(0);
    });
  });
  describe("#angleRadians", () => {
    it("should return the correct angle in radians between two vectors", () => {
      const v1 = new Vector3(1, 0, 0);
      const v2 = new Vector3(0, 1, 0);
      const result = v1.angleRadians(v2);
      expect(result).toBeCloseTo(Math.PI / 2);
    });
  });
  describe("#angleDegrees", () => {
    it("should return the correct angle in degrees between two vectors", () => {
      const v1 = new Vector3(1, 0, 0);
      const v2 = new Vector3(0, 1, 0);
      const result = v1.angleDegrees(v2);
      expect(result).toBeCloseTo(90);
    });
  });
});
