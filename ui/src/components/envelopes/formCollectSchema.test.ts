import { describe, expect, it } from "vitest";
import { repairFormSchema } from "./formCollectSchema";

describe("repairFormSchema", () => {
  it("adopts flat fields that name a section and reads name as id", () => {
    const out = repairFormSchema({
      answers: { a: "x" },
      sections: [{ id: "policy", title: "Policy" }],
      fields: [
        { name: "a", label: "A", type: "select", section: "policy", options: [{ value: "x", label: "X" }] },
        { name: "b", type: "textarea" },
      ],
    });
    expect(out.sections?.[0].fields.map((f) => f.id)).toEqual(["a"]);
    expect(out.fields?.map((f) => [f.id, f.label])).toEqual([["b", "b"]]);
  });

  it("gives a section without fields an empty list instead of crashing", () => {
    const out = repairFormSchema({ sections: [{ id: "s", title: "S" }, "junk", null] });
    expect(out.sections).toHaveLength(1);
    expect(out.sections?.[0].fields).toEqual([]);
  });

  it("leaves a well-formed schema's fields and sections intact", () => {
    const schema = {
      sections: [{ id: "s", title: "S", fields: [{ id: "f", type: "text", label: "F" }] }],
    };
    const out = repairFormSchema(schema);
    expect(out.sections?.[0].fields).toEqual(schema.sections[0].fields);
    expect(out.fields).toEqual([]);
  });

  it("tolerates a missing or non-object schema", () => {
    expect(repairFormSchema(undefined)).toEqual({});
    expect(repairFormSchema("x")).toEqual({});
  });
});
