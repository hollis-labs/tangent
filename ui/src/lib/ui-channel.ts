import { z } from "zod";

export type UiScope = "ephemeral" | "url-backed";
export type UiDeclaration = { name: string; scope: UiScope; input_schema: Record<string, unknown> };
export type ViewDescriptor = {
  version: 1;
  route: string;
  active_filters?: { name: string; values: string[] }[];
  search?: string;
  selected_ids?: string[];
  visible_rows?: { id: string; summary: string }[];
  available_commands?: UiDeclaration[];
};
export const UiCommandSchema = z
  .object({
    type: z.literal("ui.command"),
    command_id: z.string().min(1).max(128),
    view_revision: z.number().int().positive(),
    name: z.string().min(1).max(128),
    scope: z.enum(["ephemeral", "url-backed"]),
    arguments: z.record(z.string(), z.unknown()),
  })
  .strict();
export const ViewPublishedSchema = z
  .object({
    type: z.literal("view.published"),
    view_revision: z.number().int().positive(),
  })
  .strict();
export type UiCommand = z.infer<typeof UiCommandSchema>;
export type UiEvent =
  | UiCommand
  | z.infer<typeof ViewPublishedSchema>
  | { type: "attached" | "detached" | "refused" };
export type UiTransport = {
  connected: () => boolean;
  send: (frame: object) => boolean;
  subscribe: (listener: (event: UiEvent) => void) => () => void;
};
export type UiResult = "applied" | "not_visible" | "rejected";
export type UiHandler = {
  declaration: UiDeclaration;
  validate: (args: Record<string, unknown>) => boolean;
  apply: (args: Record<string, unknown>) => UiResult;
};
const idSchema = {
  type: "object",
  properties: { id: { type: "string", minLength: 1, maxLength: 256 } },
  required: ["id"],
  additionalProperties: false,
};
export function idCommand(name: string, scope: UiScope, apply: UiHandler["apply"]): UiHandler {
  return {
    declaration: { name, scope, input_schema: idSchema },
    validate: (args) =>
      Object.keys(args).length === 1 &&
      typeof args.id === "string" &&
      args.id.length > 0 &&
      args.id.length <= 256,
    apply,
  };
}
export function navigateCommand(navigate: (route: string) => void): UiHandler {
  return {
    declaration: {
      name: "navigate",
      scope: "url-backed",
      input_schema: {
        type: "object",
        properties: { route: { type: "string", maxLength: 2048 } },
        required: ["route"],
        additionalProperties: false,
      },
    },
    validate: (args) =>
      Object.keys(args).length === 1 &&
      typeof args.route === "string" &&
      /^\/(?!\/)[^\\\s\p{Cc}]*$/u.test(args.route) &&
      args.route.length <= 2048 &&
      /^\/(?:inbox(?:\/items\/[^/]+)?|r\/[^/]+|channels(?:\/[^/]+)?|settings)?$/.test(
        new URL(args.route, "http://tangent.invalid").pathname,
      ),
    apply: (args) => {
      navigate(args.route as string);
      return "applied";
    },
  };
}

const DescriptorShape = z
  .object({
    version: z.literal(1),
    route: z.string(),
    search: z.string().optional(),
    active_filters: z
      .array(z.object({ name: z.string(), values: z.array(z.string()) }).strict())
      .optional(),
    selected_ids: z.array(z.string()).optional(),
    visible_rows: z.array(z.object({ id: z.string(), summary: z.string() }).strict()).optional(),
    available_commands: z
      .array(
        z
          .object({
            name: z.string(),
            scope: z.enum(["ephemeral", "url-backed"]),
            input_schema: z.record(z.string(), z.unknown()),
          })
          .strict(),
      )
      .optional(),
  })
  .strict();

const bytes = (value: string) => new TextEncoder().encode(value).length;
const identifier = (value: string, max: number) =>
  value.length > 0 && bytes(value) <= max && !/[\s\p{Cc}\p{Cf}\p{Cs}]/u.test(value);
const display = (value: string, max: number) =>
  bytes(value) <= max &&
  !/[\p{Cc}\p{Cf}\p{Cs}]/u.test(value.replaceAll("\n", "").replaceAll("\t", ""));
/** Reject a whole observation rather than silently truncating user state. */
export function validDescriptor(d: ViewDescriptor): boolean {
  try {
    if (!DescriptorShape.safeParse(d).success) return false;
    const raw = JSON.stringify(d);
    if (
      bytes(raw) > 32768 ||
      d.version !== 1 ||
      !identifier(d.route, 256) ||
      !/^\/(?!\/)[^\\?#]*$/.test(d.route)
    )
      return false;
    let nodes = 0;
    const walk = (value: unknown, depth: number): boolean => {
      if (++nodes > 2048 || depth > 16) return false;
      if (value !== null && typeof value === "object")
        return Object.values(value).every((child) => walk(child, depth + 1));
      return true;
    };
    if (!walk(d, 0)) return false;
    const unique = (values: string[]) => new Set(values).size === values.length;
    const filters = d.active_filters ?? [],
      ids = d.selected_ids ?? [],
      rows = d.visible_rows ?? [],
      commands = d.available_commands ?? [];
    return (
      filters.length <= 16 &&
      unique(filters.map((f) => f.name)) &&
      filters.every(
        (f) =>
          identifier(f.name, 64) &&
          f.values.length > 0 &&
          f.values.length <= 16 &&
          f.values.every((v) => display(v, 256)),
      ) &&
      display(d.search ?? "", 1024) &&
      ids.length <= 64 &&
      unique(ids) &&
      ids.every((id) => identifier(id, 128)) &&
      rows.length <= 32 &&
      unique(rows.map((r) => r.id)) &&
      rows.every((r) => identifier(r.id, 128) && display(r.summary, 512)) &&
      commands.length <= 16 &&
      unique(commands.map((c) => c.name)) &&
      commands.every(
        (c) =>
          identifier(c.name, 128) &&
          /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)?$/.test(c.name) &&
          bytes(JSON.stringify(c.input_schema)) <= 2048,
      )
    );
  } catch {
    return false;
  }
}
