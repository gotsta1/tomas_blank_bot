export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  if (!(options.body instanceof FormData))
    headers.set("Content-Type", "application/json");
  const response = await fetch("/api" + path, {
    ...options,
    credentials: "same-origin",
    headers,
  });
  const contentType = response.headers.get("content-type") || "";
  let data: Record<string, unknown> = {};
  if (contentType.includes("application/json")) {
    data = (await response.json()) as Record<string, unknown>;
  } else {
    const body = await response.text();
    if (body)
      data.error = body
        .replace(/<[^>]*>/g, " ")
        .replace(/\s+/g, " ")
        .trim();
  }
  if (!response.ok) {
    if (response.status === 401)
      window.dispatchEvent(new Event("session-expired"));
    throw new APIError(
      response.status,
      response.status === 413
        ? "Файл слишком большой"
        : typeof data.error === "string" && data.error
          ? data.error
          : "Не удалось выполнить запрос",
    );
  }
  return data as T;
}
export type Button = {
  label: string;
  type: "callback" | "url";
  action: string;
  row: number;
  enabled: boolean;
};
export type Message = {
  id: number;
  key: string;
  name: string;
  text: string;
  parse_mode: "" | "HTML";
  buttons: Button[];
  version: number;
  media: MessageMedia[];
};
export type MessageMedia = {
  id: number;
  type: "photo" | "video" | "video_note";
  position: "before" | "after";
  sort_order: number;
  name: string;
  size: number;
  file?: File;
  preview_url?: string;
};

// Media files are uploaded separately; drafts also contain browser-only File
// and preview_url values that must never be sent in message JSON.
export function messagePayload(message: Message): Omit<Message, "media"> {
  return {
    id: message.id,
    key: message.key,
    name: message.name,
    text: message.text,
    parse_mode: message.parse_mode,
    buttons: message.buttons,
    version: message.version,
  };
}

export function uploadMessageMedia(
  messageID: number,
  media: Pick<MessageMedia, "type" | "position">,
  file: File,
) {
  const form = new FormData();
  form.set("media_type", media.type);
  form.set("media_position", media.position);
  form.set("file", file);
  return api<{ media: MessageMedia }>(`/messages/${messageID}/media`, {
    method: "POST",
    body: form,
  });
}

export type Step = {
  id: number;
  kind: "message" | "delay";
  message_id: number;
  position: number;
  delay_seconds: number;
  enabled: boolean;
};
