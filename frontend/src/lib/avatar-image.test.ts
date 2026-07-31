import { describe, expect, it } from "vitest";
import { scaleToFit } from "./avatar-image";

describe("scaleToFit", () => {
  it("leaves an image that already fits alone", () => {
    expect(scaleToFit({ width: 200, height: 120 }, 512)).toEqual({
      width: 200,
      height: 120,
    });
  });

  it("scales a landscape photo by its longest side", () => {
    expect(scaleToFit({ width: 4000, height: 3000 }, 512)).toEqual({
      width: 512,
      height: 384,
    });
  });

  it("scales a portrait photo by its longest side", () => {
    expect(scaleToFit({ width: 3000, height: 4000 }, 512)).toEqual({
      width: 384,
      height: 512,
    });
  });

  it("keeps a square square", () => {
    expect(scaleToFit({ width: 2048, height: 2048 }, 512)).toEqual({
      width: 512,
      height: 512,
    });
  });

  // A panorama's short side must not round away to zero: a canvas of width or
  // height 0 encodes to nothing.
  it("never rounds a side below one pixel", () => {
    expect(scaleToFit({ width: 10000, height: 3 }, 512)).toEqual({
      width: 512,
      height: 1,
    });
  });
});
