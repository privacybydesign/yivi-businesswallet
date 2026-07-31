import { describe, expect, it } from "vitest";
import type { MailTemplate, MailTemplateVariable } from "../api/email";
import {
  MAX_MAIL_BLOCKS,
  buttonUrlShapeIsValid,
  hasMalformedPlaceholder,
  insertPlaceholder,
  mailTemplatesDiffer,
  newMailBlock,
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
    blocks: [
      { ...newMailBlock("logo") },
      { ...newMailBlock("heading"), text: "Join {{orgName}}" },
      { ...newMailBlock("paragraph"), text: "{{orgName}} invited you." },
      {
        ...newMailBlock("button"),
        label: "Accept the invitation",
        url: "{{acceptUrl}}",
      },
      { ...newMailBlock("footer"), text: "Sent by {{orgName}}." },
    ],
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
  it("accepts a layout that only references declared variables", () => {
    expect(validateMailTemplate(template(), INVITATION_VARIABLES)).toEqual([]);
  });

  it("names an undeclared placeholder and the block it is in", () => {
    const broken = template();
    broken.blocks[1] = {
      ...broken.blocks[1],
      text: "Hello {{recipientName}}",
    };
    expect(validateMailTemplate(broken, INVITATION_VARIABLES)).toEqual([
      {
        field: "blocks",
        issue: "unknownPlaceholder",
        placeholder: "recipientName",
        blockIndex: 1,
        blockField: "text",
      },
    ]);
  });

  it("requires a subject", () => {
    const problems = validateMailTemplate(
      template({ subject: "   " }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([{ field: "subject", issue: "required" }]);
  });

  it("requires at least one block", () => {
    const problems = validateMailTemplate(
      template({ blocks: [] }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([{ field: "blocks", issue: "noBlocks" }]);
  });

  it("requires at least one heading or paragraph", () => {
    const problems = validateMailTemplate(
      template({
        blocks: [
          newMailBlock("logo"),
          { ...newMailBlock("footer"), text: "Small print" },
        ],
      }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([{ field: "blocks", issue: "needsContent" }]);
  });

  it("caps the block count at the backend's maximum", () => {
    const blocks = Array.from({ length: MAX_MAIL_BLOCKS + 1 }, () => ({
      ...newMailBlock("paragraph"),
      text: "Something to say.",
    }));
    const problems = validateMailTemplate(
      template({ blocks }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([{ field: "blocks", issue: "tooManyBlocks" }]);
  });

  it("requires text on a heading, paragraph or footer block", () => {
    const problems = validateMailTemplate(
      template({
        blocks: [
          { ...newMailBlock("heading"), text: "  " },
          { ...newMailBlock("paragraph"), text: "Fine" },
        ],
      }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      { field: "blocks", issue: "required", blockIndex: 0, blockField: "text" },
    ]);
  });

  it("requires both a label and a URL on a button block", () => {
    const problems = validateMailTemplate(
      template({
        blocks: [
          { ...newMailBlock("heading"), text: "Hi" },
          newMailBlock("button"),
        ],
      }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      {
        field: "blocks",
        issue: "required",
        blockIndex: 1,
        blockField: "label",
      },
      { field: "blocks", issue: "required", blockIndex: 1, blockField: "url" },
    ]);
  });

  it("flags an unsafe button URL on the block", () => {
    const broken = template();
    broken.blocks[3] = { ...broken.blocks[3], url: "javascript:alert(1)" };
    expect(validateMailTemplate(broken, INVITATION_VARIABLES)).toEqual([
      {
        field: "blocks",
        issue: "buttonUrlShape",
        blockIndex: 3,
        blockField: "url",
      },
    ]);
  });

  it("flags a malformed placeholder in the subject", () => {
    const problems = validateMailTemplate(
      template({ subject: "Code: {{ tx code }}" }),
      INVITATION_VARIABLES,
    );
    expect(problems).toEqual([
      { field: "subject", issue: "malformedPlaceholder" },
    ]);
  });
});

describe("buttonUrlShapeIsValid", () => {
  it("accepts a single URL variable and an absolute http(s) literal", () => {
    expect(buttonUrlShapeIsValid("{{acceptUrl}}", INVITATION_VARIABLES)).toBe(
      true,
    );
    expect(
      buttonUrlShapeIsValid(
        "https://wallet.example.org/join",
        INVITATION_VARIABLES,
      ),
    ).toBe(true);
  });

  it("refuses a non-URL variable, a mix, a relative path and a javascript: URL", () => {
    expect(buttonUrlShapeIsValid("{{orgName}}", INVITATION_VARIABLES)).toBe(
      false,
    );
    expect(
      buttonUrlShapeIsValid(
        "https://example.org/{{acceptUrl}}",
        INVITATION_VARIABLES,
      ),
    ).toBe(false);
    expect(buttonUrlShapeIsValid("/join", INVITATION_VARIABLES)).toBe(false);
    expect(
      buttonUrlShapeIsValid("javascript:alert(1)", INVITATION_VARIABLES),
    ).toBe(false);
    expect(
      buttonUrlShapeIsValid("data:text/html,<script>", INVITATION_VARIABLES),
    ).toBe(false);
  });

  it("refuses a placeholder that is not a declared variable", () => {
    expect(buttonUrlShapeIsValid("{{nope}}", INVITATION_VARIABLES)).toBe(false);
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

  it("is true for an edited subject, an added block or an edited block", () => {
    expect(
      mailTemplatesDiffer(template(), template({ subject: "Changed" })),
    ).toBe(true);

    const added = template();
    added.blocks = [...added.blocks, newMailBlock("divider")];
    expect(mailTemplatesDiffer(template(), added)).toBe(true);

    const edited = template();
    edited.blocks[2] = { ...edited.blocks[2], text: "Changed" };
    expect(mailTemplatesDiffer(template(), edited)).toBe(true);
  });

  it("is true when blocks are reordered", () => {
    const reordered = template();
    [reordered.blocks[1], reordered.blocks[2]] = [
      reordered.blocks[2],
      reordered.blocks[1],
    ];
    expect(mailTemplatesDiffer(template(), reordered)).toBe(true);
  });
});
