import { SUPPORTED_CDP_DOMAINS } from "./debugger.ts";

export const PROTOCOL_VERSION = "1.0";

export type BridgeRequest = {
  jsonrpc: "2.0";
  id: string | number;
  method: string;
  params?: Record<string, unknown>;
};

export function makeHello(profileId: string, extensionVersion: string) {
  return { kind: "hello" as const, profileId, protocolVersion: PROTOCOL_VERSION, extensionVersion, supportedCdpDomains: [...SUPPORTED_CDP_DOMAINS] };
}

export function parseRequest(value: unknown): BridgeRequest | null {
  if (!value || typeof value !== "object") return null;
  const candidate = value as Record<string, unknown>;
  if (candidate.jsonrpc !== "2.0" || (typeof candidate.id !== "string" && typeof candidate.id !== "number")) return null;
  if (typeof candidate.method !== "string" || candidate.method.length === 0) return null;
  if (candidate.params !== undefined && (!candidate.params || typeof candidate.params !== "object" || Array.isArray(candidate.params))) return null;
  return candidate as BridgeRequest;
}

export function errorResponse(id: string | number | null, kind: string, message: string, retryable: boolean) {
  return {
    jsonrpc: "2.0" as const,
    id,
    error: { code: -32000, message, data: { kind, retryable } },
  };
}
