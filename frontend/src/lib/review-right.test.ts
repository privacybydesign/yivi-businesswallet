import { describe, expect, it } from "vitest";
import {
  reviewRightChanged,
  reviewRightShown,
  showsReviewTag,
} from "./review-right";

const member = { role: "member", canReview: false };
const reviewer = { role: "member", canReview: true };
const admin = { role: "admin", canReview: false };

describe("reviewRightShown", () => {
  it("is on for an admin whatever was saved", () => {
    expect(reviewRightShown(admin)).toBe(true);
  });

  it("follows the box for a member", () => {
    expect(reviewRightShown(member)).toBe(false);
    expect(reviewRightShown(reviewer)).toBe(true);
  });
});

describe("reviewRightChanged", () => {
  it("is false while the box is as saved", () => {
    expect(reviewRightChanged(member, member)).toBe(false);
    expect(reviewRightChanged(reviewer, reviewer)).toBe(false);
  });

  it("is true when a member is given or loses the right", () => {
    expect(reviewRightChanged(member, reviewer)).toBe(true);
    expect(reviewRightChanged(reviewer, member)).toBe(true);
  });

  it("is false for an admin, whose box cannot change", () => {
    expect(reviewRightChanged(member, { role: "admin", canReview: true })).toBe(
      false,
    );
  });

  it("is true for an admin made a member with the box changed", () => {
    expect(reviewRightChanged(admin, reviewer)).toBe(true);
  });
});

describe("showsReviewTag", () => {
  it("names the right on a member who has it", () => {
    expect(showsReviewTag(reviewer)).toBe(true);
    expect(showsReviewTag(member)).toBe(false);
  });

  it("leaves an admin to their role", () => {
    expect(showsReviewTag({ role: "admin", canReview: true })).toBe(false);
  });
});
