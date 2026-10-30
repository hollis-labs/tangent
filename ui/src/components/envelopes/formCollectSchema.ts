import type { FormCollectField, FormCollectSchema, FormCollectSection } from "./FormCollect";

// Agents have sent form schemas in the wrong shape: fields keyed by `name`
// instead of `id`, flat `fields` that point at a section with a `section`
// string, and sections with no `fields` array. The host accepted them and the
// form then threw "fields is not iterable", which left the whole inbox item
// unreviewable. The tool input schema now rejects those shapes; this repairs
// the ones already stored so the operator can still answer them.
type Loose = Record<string, unknown>;

const isObject = (v: unknown): v is Loose => typeof v === "object" && v !== null && !Array.isArray(v);

function repairField(raw: unknown): (FormCollectField & { section?: string }) | null {
  if (!isObject(raw)) return null;
  const id = typeof raw.id === "string" && raw.id ? raw.id : typeof raw.name === "string" ? raw.name : "";
  if (!id) return null;
  const { name: _name, ...rest } = raw;
  return {
    ...(rest as Loose),
    id,
    type: (typeof raw.type === "string" ? raw.type : "text") as FormCollectField["type"],
    label: typeof raw.label === "string" && raw.label ? raw.label : id,
  } as FormCollectField & { section?: string };
}

export function repairFormSchema(input: unknown): FormCollectSchema {
  if (!isObject(input)) return {};
  const flat = (Array.isArray(input.fields) ? input.fields : [])
    .map(repairField)
    .filter((f): f is FormCollectField & { section?: string } => f !== null);
  const rawSections = Array.isArray(input.sections) ? input.sections.filter(isObject) : [];
  const sectionIDs = new Set(rawSections.map((s) => String(s.id ?? "")));

  const sections: FormCollectSection[] = rawSections.map((s, i) => {
    const id = typeof s.id === "string" && s.id ? s.id : `section-${i + 1}`;
    const own = (Array.isArray(s.fields) ? s.fields : [])
      .map(repairField)
      .filter((f): f is FormCollectField & { section?: string } => f !== null);
    const adopted = flat.filter((f) => f.section === id);
    return {
      ...(s as Loose),
      id,
      title: typeof s.title === "string" && s.title ? s.title : id,
      fields: [...own, ...adopted].map(({ section: _section, ...f }) => f as FormCollectField),
    } as FormCollectSection;
  });

  const loose = flat
    .filter((f) => !f.section || !sectionIDs.has(f.section))
    .map(({ section: _section, ...f }) => f as FormCollectField);

  return { ...(input as Loose), fields: loose, sections } as FormCollectSchema;
}
