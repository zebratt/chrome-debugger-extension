export type AXNode = {
  nodeId?: string; parentId?: string; childIds?: string[]; backendDOMNodeId?: number; ignored?: boolean;
  role?: { value?: unknown }; name?: { value?: unknown }; value?: { value?: unknown };
  properties?: { name: string; value: { value?: unknown } }[];
};
export type Send = (method: string, params: Record<string, unknown>) => Promise<unknown>;
const textInputTypes = new Set(["text", "search", "email", "tel", "url", "textarea"]);
export const inputRoles = new Set(["textbox", "searchbox", "combobox"]);
export function property(node: AXNode, name: string): unknown {
  return node.properties?.find((entry) => entry.name === name)?.value.value;
}
export async function describeInput(send: Send, backendNodeId: number) {
  try {
    const result = await send("DOM.describeNode", { backendNodeId }) as { node?: { nodeName?: string; attributes?: string[] } };
    const attributes = result.node?.attributes || [];
    const attrs = new Map<string, string>();
    for (let i = 0; i + 1 < attributes.length; i += 2) attrs.set(attributes[i].toLowerCase(), attributes[i + 1]);
    const tag = result.node?.nodeName?.toUpperCase();
    const inputType = tag === "INPUT" ? (attrs.get("type") || "text").toLowerCase() : tag === "TEXTAREA" ? "textarea" : "unknown";
    const disabled = attrs.has("disabled") || attrs.get("aria-disabled") === "true";
    return { inputType, disabled, editable: textInputTypes.has(inputType) && !disabled && !attrs.has("readonly") && attrs.get("aria-readonly") !== "true" };
  } catch { return { inputType: "unknown", disabled: false, editable: false }; }
}
export function nodeContext(node: AXNode, all: Map<string, AXNode>): string {
  const parts: string[] = [];
  let parent = node.parentId ? all.get(node.parentId) : undefined;
  for (let depth = 0; parent && depth < 3; depth++) {
    if (parent.role?.value === "RootWebArea") break;
    const name = String(parent.name?.value || "").trim();
    if (name) parts.push(name.slice(0, 180));
    const labels: string[] = [];
    for (const id of parent.childIds || []) {
      const sibling = all.get(id);
      if (!sibling || sibling.nodeId === node.nodeId) continue;
      const role = String(sibling.role?.value || "");
      if (["StaticText", "heading", "caption"].includes(role)) labels.push(String(sibling.name?.value || ""));
      else if (role === "paragraph") {
        labels.push(...(sibling.childIds || []).map((child) => all.get(child)).filter((child) => child?.role?.value === "StaticText").map((child) => String(child?.name?.value || "")));
      }
      if (labels.length >= 3) break;
    }
    parts.push(...labels.slice(0, 3).map((label) => label.slice(0, 180)));
    parent = parent.parentId ? all.get(parent.parentId) : undefined;
  }
  return [...new Set(parts.filter(Boolean))].join(" / ").slice(0, 360);
}
