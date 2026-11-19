import type { Member } from "../api/organization";

const ADMIN_ROLE = "admin";

type ReviewRightHolder = Pick<Member, "role" | "canReview">;

// An admin reviews without the right: it is a member's to be given.
export function reviewRightLocked(role: string): boolean {
  return role === ADMIN_ROLE;
}

// What the "Can review" box shows: on for an admin, who always can.
export function reviewRightShown(form: ReviewRightHolder): boolean {
  return reviewRightLocked(form.role) || form.canReview;
}

// Whether a save sends the right: only a member's, and only a changed one.
export function reviewRightChanged(
  saved: ReviewRightHolder,
  form: ReviewRightHolder,
): boolean {
  return !reviewRightLocked(form.role) && form.canReview !== saved.canReview;
}

// Whether a member's row names the right. An admin's role already says it.
export function showsReviewTag(member: ReviewRightHolder): boolean {
  return !reviewRightLocked(member.role) && member.canReview;
}
