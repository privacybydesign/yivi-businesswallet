import { describe, expect, it } from "vitest";
import { returnLinkHost } from "./return-link";

describe("returnLinkHost", () => {
  it("names the host of the site to return to", () => {
    expect(
      returnLinkHost(
        "https://shop.example.nl/orders/7?request_id=abc&status=signed",
      ),
    ).toBe("shop.example.nl");
    expect(returnLinkHost("http://localhost:3000/done")).toBe("localhost");
  });

  it("offers no link without a return address", () => {
    expect(returnLinkHost(undefined)).toBeNull();
    expect(returnLinkHost("")).toBeNull();
  });

  it("offers no link for anything but an absolute web address", () => {
    expect(returnLinkHost("javascript:alert(1)")).toBeNull();
    expect(returnLinkHost("data:text/html,<p>hi</p>")).toBeNull();
    expect(returnLinkHost("/orders/7")).toBeNull();
    expect(returnLinkHost("//shop.example.nl/orders/7")).toBeNull();
    expect(returnLinkHost("shop.example.nl")).toBeNull();
  });
});
