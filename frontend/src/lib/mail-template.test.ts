import { describe, expect, it } from "vitest";
import type { MailTemplate, MailTemplateVariable } from "../api/email";
import {
  ctaUrlShapeIsValid,
  hasMalformedPlaceholder,
  insertPlaceholder,
  mailTemplatesDiffer,
  placeholdersIn,
  validateMailTemplate,
} from "./mail-template";

const INVITATION_VARIABLES: MailTemplateVariable[] = [
  { name: "orgName", isUrl: false },
  { name: "acceptUrl", isUrl: true },
];

function template(overrides: Partial<MailTemplate> = {}): MailTemplate {
  return {
    subject: "You have been invited to join {{orgName}}",
    preheader: "",
    headline: "Join {{orgName}}",
    paragraphs: ["{{orgName}} invited you."],
    ctaLabel: "Accept the invitation",
    ctaUrl: "{{acceptUrl}}",
    linkFallback: "",
    note: "",
    footer: "",
    ...overrides,
  };
}

describe("placeholdersIn", () => {
  it("finds every placeholder, tolerating inner spaces", () => {
    expect(placeholdersIn("Hi {{orgName}}, see {{ acceptUrl }}")).toEqual([
      "orgName",
      "acceptUrl",
    ]);
  });

  it("finds nothing in plain prose", () => {
    expect(placeholdersIn("No placeholders here")).toEqual([]);
  });
});

describe("hasMalformedPlaceholder", () => {
  it("flags braces the placeholder syntax does not cover", () => {
    expect(hasMalformedPlaceholder("Hello {{ org name }}")).toBe(true);
    expect(hasMalformedPlaceholder("Hello {{orgName}}")).toBe(false);
  });
});

describe("validateMailTemplate", () => {
  it("accepts a template that only references declared variables", () => {
    expect(validateMailTemplate(template(), INVITATION_VARIABLES)).toEqual([]);
  });

  it("names an undeclared placeholder and the field it is in", () => {
    const problems = validateMailTemplate(
      template({ headline: "Hello {{recipientName}}" }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      {
        field: "headline",
        issue: "unknownPlaceholder",
        placeholder: "recipientName",
        paragraphIndex: undefined,
      },
    ]);
  });

  it("reports which paragraph an undeclared placeholder is in", () => {
    const problems = validateMailTemplate(
      template({ paragraphs: ["Fine {{orgName}}", "Broken {{nope}}"] }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      {
        field: "paragraphs",
        issue: "unknownPlaceholder",
        placeholder: "nope",
        paragraphIndex: 1,
      },
    ]);
  });

  it("requires a subject and a headline", () => {
    const problems = validateMailTemplate(
      template({ subject: "   ", headline: "" }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      { field: "subject", issue: "required" },
      { field: "headline", issue: "required" },
    ]);
  });

  it("requires the call-to-action label and URL together", () => {
    const problems = validateMailTemplate(
      template({ ctaLabel: "Accept", ctaUrl: "" }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([{ field: "ctaUrl", issue: "ctaPair" }]);
  });

  it("flags a malformed placeholder", () => {
    const problems = validateMailTemplate(
      template({ note: "Code: {{ tx code }}" }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      {
        field: "note",
        issue: "malformedPlaceholder",
        paragraphIndex: undefined,
      },
    ]);
  });
});

describe("ctaUrlShapeIsValid", () => {
  it("accepts an empty value, a single URL variable and an absolute http(s) literal", () => {
    expect(ctaUrlShapeIsValid("", INVITATION_VARIABLES)).toBe(true);
    expect(ctaUrlShapeIsValid("{{acceptUrl}}", INVITATION_VARIABLES)).toBe(
      true,
    );
    expect(
      ctaUrlShapeIsValid(
        "https://wallet.example.org/join",
        INVITATION_VARIABLES,
      ),
    ).toBe(true);
  });

  it("refuses a non-URL variable, a mix, a relative path and a javascript: URL", () => {
    expect(ctaUrlShapeIsValid("{{orgName}}", INVITATION_VARIABLES)).toBe(false);
    expect(
      ctaUrlShapeIsValid(
        "https://example.org/{{acceptUrl}}",
        INVITATION_VARIABLES,
      ),
    ).toBe(false);
    expect(ctaUrlShapeIsValid("/join", INVITATION_VARIABLES)).toBe(false);
    expect(
      ctaUrlShapeIsValid("javascript:alert(1)", INVITATION_VARIABLES),
    ).toBe(false);
    expect(
      ctaUrlShapeIsValid("data:text/html,<script>", INVITATION_VARIABLES),
    ).toBe(false);
  });

  it("refuses a placeholder that is not a declared variable", () => {
    expect(ctaUrlShapeIsValid("{{nope}}", INVITATION_VARIABLES)).toBe(false);
  });
});

describe("insertPlaceholder", () => {
  it("inserts at the caret and reports where the caret lands", () => {
    expect(insertPlaceholder("Hello , welcome", 6, "orgName")).toEqual({
      value: "Hello {{orgName}}, welcome",
      caret: 17,
    });
  });

  it("appends when there is no caret", () => {
    expect(insertPlaceholder("Hello", null, "orgName")).toEqual({
      value: "Hello{{orgName}}",
      caret: 16,
    });
  });

  it("clamps a caret beyond the value", () => {
    expect(insertPlaceholder("Hi", 99, "orgName").value).toBe("Hi{{orgName}}");
  });
});

describe("mailTemplatesDiffer", () => {
  it("is false for an untouched draft", () => {
    expect(mailTemplatesDiffer(template(), template())).toBe(false);
  });

  it("is true for an edited field, an added paragraph or an edited paragraph", () => {
    expect(mailTemplatesDiffer(template(), template({ note: "Hi" }))).toBe(
      true,
    );
    expect(
      mailTemplatesDiffer(template(), template({ paragraphs: ["a", "b"] })),
    ).toBe(true);
    expect(
      mailTemplatesDiffer(template(), template({ paragraphs: ["changed"] })),
    ).toBe(true);
  });
});
