export const STORAGE_KEY = "qwen2api-admin-key";

export type ApiResponseEnvelope<T> = {
  ok: boolean;
  status: number;
  statusText: string;
  url: string;
  headers: Record<string, string>;
  body: T;
  rawText: string;
};

export class ApiRequestError extends Error {
  response: ApiResponseEnvelope<unknown>;

  constructor(message: string, response: ApiResponseEnvelope<unknown>) {
    super(message);
    this.name = "ApiRequestError";
    this.response = response;
  }
}

export async function apiRequest<T>(
  path: string,
  options: RequestInit = {},
  apiKey?: string,
): Promise<T> {
  const response = await apiRequestEnvelope<T>(path, options, apiKey);
  return response.body;
}

export async function apiRequestEnvelope<T>(
  path: string,
  options: RequestInit = {},
  apiKey?: string,
): Promise<ApiResponseEnvelope<T>> {
  const isFormData = typeof FormData !== "undefined" && options.body instanceof FormData;
  const response = await fetch(path, {
    ...options,
    headers: {
      ...(isFormData ? {} : { "Content-Type": "application/json" }),
      ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}),
      ...(options.headers || {}),
    },
    cache: "no-store",
  });

  const text = await response.text();
  const data = parseResponseText(text);
  const envelope: ApiResponseEnvelope<T> = {
    ok: response.ok,
    status: response.status,
    statusText: response.statusText,
    url: response.url,
    headers: Object.fromEntries(response.headers.entries()),
    body: data as T,
    rawText: text,
  };

  if (!response.ok) {
    const message =
      typeof data === "object" && data !== null
        ? extractErrorMessage(data, response.status)
        : `请求失败 (${response.status})`;
    throw new ApiRequestError(message, envelope as ApiResponseEnvelope<unknown>);
  }

  return envelope;
}

function parseResponseText(text: string) {
  if (!text) {
    return {};
  }

  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function extractErrorMessage(data: unknown, status: number) {
  if (typeof data !== "object" || data === null) {
    return `请求失败 (${status})`;
  }

  const payload = data as { error?: unknown; message?: unknown };
  if (typeof payload.message === "string" && payload.message.trim()) {
    return payload.message;
  }
  if (typeof payload.error === "string" && payload.error.trim()) {
    return payload.error;
  }
  if (typeof payload.error === "object" && payload.error !== null) {
    const nested = payload.error as { message?: unknown; error?: unknown };
    if (typeof nested.message === "string" && nested.message.trim()) {
      return nested.message;
    }
    if (typeof nested.error === "string" && nested.error.trim()) {
      return nested.error;
    }
  }
  return `请求失败 (${status})`;
}
