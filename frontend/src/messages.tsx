import { type ReactNode, useEffect, useRef, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Divider,
  MenuItem,
  Paper,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  api,
  messagePayload,
  uploadMessageMedia,
  type Message,
  type MessageMedia,
  type Button as MessageButton,
} from "./api";
import { RichTextEditor } from "./richTextEditor";
export const actions = [
  "club:join",
  "club:about",
  "plan:monthly",
  "plan:90d",
  "plan:180d",
  "plan:year",
  "pay:region:ru",
  "pay:region:foreign",
  "pay:ru:sbp",
  "pay:ru:card",
  "pay:foreign:paypal",
  "pay:foreign:card",
  "pay:foreign:applepay",
  "pay:check",
  "nav:start",
  "nav:plan",
  "nav:region",
  "subscription:status",
  "subscription:cancel",
  "subscription:cancel:confirm",
];

const previewExamples: Record<string, string> = {
  plan_name: "Подписка Match+",
  price: "15 USD",
  period: "3 месяца",
  expires_date: "22.10.2026",
  days_left: "28",
  auto_renew: "включено",
  payment_url: "https://pay.lava.top/example",
  invite_url: "https://t.me/+example",
};

const templateVariableHints = [
  {
    name: "plan_name",
    description: "Название выбранного тарифа.",
    example: "Подписка Match+",
  },
  {
    name: "price",
    description: "Стоимость тарифа с валютой.",
    example: "1 490 ₽ / 15 USD",
  },
  {
    name: "period",
    description: "Оплаченный период подписки.",
    example: "3 месяца",
  },
  {
    name: "expires_date",
    description: "Дата окончания доступа без времени и часового пояса.",
    example: "22.10.2026",
  },
  {
    name: "days_left",
    description: "Количество дней до окончания доступа.",
    example: "28",
  },
  {
    name: "auto_renew",
    description: "Текущий статус автоматического продления.",
    example: "включено / выключено",
  },
] as const;

export function TemplateVariablesHelp() {
  return (
    <Stack
      direction="row"
      spacing={0.5}
      useFlexGap
      flexWrap="wrap"
      alignItems="center"
    >
      <Typography variant="caption" color="text.secondary">
        Переменные:
      </Typography>
      {templateVariableHints.map((variable, index) => (
        <Box component="span" key={variable.name}>
          <Tooltip
            arrow
            enterDelay={250}
            title={
              <Box sx={{ py: 0.25 }}>
                <Typography variant="caption" fontWeight={700} display="block">
                  {`{{${variable.name}}}`}
                </Typography>
                <Typography variant="caption" display="block">
                  {variable.description}
                </Typography>
                <Typography variant="caption" display="block" sx={{ mt: 0.5 }}>
                  Пример: {variable.example}
                </Typography>
              </Box>
            }
            slotProps={{
              tooltip: {
                sx: { maxWidth: 240, px: 1.25, py: 0.75 },
              },
            }}
          >
            <Box
              component="span"
              tabIndex={0}
              sx={{
                color: "text.secondary",
                cursor: "help",
                fontFamily: "monospace",
                fontSize: "0.75rem",
                outline: "none",
                borderBottom: "1px dotted currentColor",
                "&:focus-visible": {
                  color: "primary.main",
                  borderBottomColor: "primary.main",
                },
              }}
            >
              {`{{${variable.name}}}`}
            </Box>
          </Tooltip>
          {index < templateVariableHints.length - 1 && (
            <Typography
              component="span"
              variant="caption"
              color="text.secondary"
            >
              ,
            </Typography>
          )}
        </Box>
      ))}
    </Stack>
  );
}

function preview(text: string) {
  return text.replace(
    /\{\{([a-z_]+)\}\}/g,
    (_, name: string) => previewExamples[name] ?? "пример",
  );
}

function previewText(text: string, key: string): ReactNode[] {
  return text
    .split("\n")
    .flatMap((line, index) => [
      ...(index === 0 ? [] : [<br key={`${key}-break-${index}`} />]),
      line,
    ]);
}

function PreviewMediaFile({
  message,
  media,
}: {
  message: Message;
  media: MessageMedia;
}) {
  const source =
    media.preview_url ||
    `/api/messages/${message.id}/media/${media.id}?v=${message.version}`;
  if (media.type === "photo") {
    return <img src={source} alt="" />;
  }
  return (
    <video
      src={source}
      autoPlay
      muted
      controls={false}
      loop
      playsInline
      disablePictureInPicture
      preload="auto"
      onLoadedMetadata={(event) => {
        event.currentTarget.defaultMuted = true;
        event.currentTarget.muted = true;
      }}
      onCanPlay={(event) => {
        void event.currentTarget.play().catch(() => undefined);
      }}
    />
  );
}

function MediaAlbumPreview({ message }: { message: Message }) {
  const album = message.media
    .filter((item) => item.type !== "video_note")
    .sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
  if (!album.length) return null;
  return (
    <div
      className={`preview-media-album preview-media-count-${Math.min(album.length, 6)}`}
      aria-label={`Медиагруппа: ${album.length}`}
    >
      {album.map((item) => (
        <div className="preview-media-tile" key={item.id}>
          <PreviewMediaFile message={message} media={item} />
          {item.type === "video" && (
            <span className="preview-video-badge">▶</span>
          )}
        </div>
      ))}
    </div>
  );
}

function VideoNotePreview({ message }: { message: Message }) {
  const note = message.media.find((item) => item.type === "video_note");
  if (!note) return null;
  return (
    <div className="preview-video-note" aria-label="Предпросмотр кружка">
      <PreviewMediaFile message={message} media={note} />
    </div>
  );
}

function telegramHTMLPreview(text: string, parseMode: "" | "HTML") {
  const rendered = preview(text);
  if (parseMode !== "HTML") return previewText(rendered, "plain");

  const document = new DOMParser().parseFromString(rendered, "text/html");
  function renderNode(element: Node, key: string): ReactNode {
    if (element.nodeType === Node.TEXT_NODE)
      return previewText(element.textContent || "", key);
    if (!(element instanceof HTMLElement)) return null;

    const children = Array.from(element.childNodes).map((child, index) =>
      renderNode(child, `${key}-${index}`),
    );
    switch (element.tagName.toLowerCase()) {
      case "b":
      case "strong":
        return <strong key={key}>{children}</strong>;
      case "i":
      case "em":
        return <em key={key}>{children}</em>;
      case "u":
      case "ins":
        return <u key={key}>{children}</u>;
      case "s":
      case "strike":
      case "del":
        return <s key={key}>{children}</s>;
      case "code":
        return <code key={key}>{children}</code>;
      case "pre":
        return <pre key={key}>{children}</pre>;
      case "blockquote":
        return <blockquote key={key}>{children}</blockquote>;
      case "br":
        return <br key={key} />;
      default:
        return children;
    }
  }

  return Array.from(document.body.childNodes).map((child, index) =>
    renderNode(child, String(index)),
  );
}

export function MessagePreview({ message }: { message: Message }) {
  return (
    <div className="preview-column">
      <div className="section-label">ПРЕДПРОСМОТР ДИАЛОГА</div>
      <div className="phone">
        <div className="phone-header">
          Клуб <span style={{ color: "#8c9a8b", fontWeight: 400 }}> · бот</span>
        </div>
        <div className="phone-body">
          {message.media.some(
            (media) =>
              media.type === "video_note" && media.position !== "after",
          ) && <VideoNotePreview message={message} />}
          <div
            className={
              message.media.some((media) => media.type !== "video_note")
                ? "preview-message-combined"
                : ""
            }
          >
            <MediaAlbumPreview message={message} />
            <div className="bubble">
              {telegramHTMLPreview(message.text, message.parse_mode)}
            </div>
          </div>
          {Object.entries(
            [...message.buttons]
              .filter((b) => b.enabled)
              .sort((a, b) => a.row - b.row)
              .reduce<Record<string, MessageButton[]>>((groups, b) => {
                (groups[b.row] ??= []).push(b);
                return groups;
              }, {}),
          ).map(([row, buttons]) => (
            <div key={row} style={{ display: "flex", gap: 3 }}>
              {buttons?.map((b, i) => (
                <div key={i} className="preview-button" style={{ flex: 1 }}>
                  {preview(b.label)}
                  {b.type === "url" ? " ↗" : ""}
                </div>
              ))}
            </div>
          ))}
          {message.media.some(
            (media) =>
              media.type === "video_note" && media.position === "after",
          ) && <VideoNotePreview message={message} />}
        </div>
      </div>
    </div>
  );
}

export function Messages() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [current, setCurrent] = useState<Message | null>(null);
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [uploadingMedia, setUploadingMedia] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [removedMediaIDs, setRemovedMediaIDs] = useState<number[]>([]);
  const nextDraftMediaID = useRef(-1);

  function releaseDraftMedia(media: MessageMedia[]) {
    for (const item of media) {
      if (item.preview_url) URL.revokeObjectURL(item.preview_url);
    }
  }

  async function load(id?: number) {
    try {
      const response = await api<Message[]>("/messages");
      const data = response
        .filter((message) => !message.key.startsWith("broadcast_step_"))
        .map((message) => ({
          ...message,
          buttons: message.buttons
            .filter((button) => button.enabled)
            .map((button) => ({ ...button, enabled: true })),
        }));
      setMessages(data);
      setCurrent((previous) => {
        if (previous) releaseDraftMedia(previous.media);
        return data.find((m) => m.id === id) || data[0] || null;
      });
      setRemovedMediaIDs([]);
      setDirty(false);
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  function update(patch: Partial<Message>) {
    if (current) {
      setCurrent({ ...current, ...patch });
      setDirty(true);
      setSuccess(false);
    }
  }
  async function save() {
    if (!current) return;
    if (
      current.media.some((media) => media.type !== "video_note") &&
      Array.from(current.text).length > 1024
    ) {
      setError("Сократи текст до 1024 символов, чтобы сохранить его с медиа");
      return;
    }
    const draft = current;
    setSaving(true);
    setUploadingMedia(true);
    setError("");
    try {
      await api("/messages/" + draft.id, {
        method: "PUT",
        body: JSON.stringify(messagePayload(draft)),
      });

      for (const mediaID of removedMediaIDs) {
        await api(`/messages/${draft.id}/media/${mediaID}`, {
          method: "DELETE",
        });
      }

      const uploadedIDs = new Map<number, number>();
      const orderedDraftMedia = [...draft.media].sort(
        (a, b) => a.sort_order - b.sort_order || a.id - b.id,
      );
      for (const media of orderedDraftMedia) {
        if (media.id >= 0 || !media.file) continue;
        const result = await uploadMessageMedia(draft.id, media, media.file);
        uploadedIDs.set(media.id, result.media.id);
      }

      const regularOrder = orderedDraftMedia
        .filter((media) => media.type !== "video_note")
        .map((media) => (media.id < 0 ? uploadedIDs.get(media.id) : media.id))
        .filter((id): id is number => id !== undefined);
      if (regularOrder.length) {
        await api(`/messages/${draft.id}/media`, {
          method: "PUT",
          body: JSON.stringify({ order: regularOrder }),
        });
      }

      const videoNote = orderedDraftMedia.find(
        (media) => media.type === "video_note" && media.id > 0,
      );
      if (videoNote) {
        await api(`/messages/${draft.id}/media/${videoNote.id}`, {
          method: "PUT",
          body: JSON.stringify({ position: videoNote.position }),
        });
      }

      await load(draft.id);
      setSuccess(true);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
      setUploadingMedia(false);
    }
  }
  function uploadMedia(
    files: File[],
    requestedType: "photo" | "video" | "video_note",
  ) {
    if (!current) return;
    if (
      requestedType === "video_note" &&
      current.media.some((item) => item.type === "video_note")
    ) {
      setError("К сообщению можно прикрепить только один кружок");
      return;
    }
    const regularCount = current.media.filter(
      (item) => item.type !== "video_note",
    ).length;
    if (requestedType !== "video_note" && regularCount + files.length > 10) {
      setError("К сообщению можно прикрепить не более 10 фото и видео");
      return;
    }
    const invalidFile = files.find((file) =>
      requestedType === "photo"
        ? !["image/jpeg", "image/png"].includes(file.type)
        : file.type !== "video/mp4",
    );
    if (invalidFile) {
      setError("Поддерживаются фотографии JPEG/PNG и видео MP4");
      return;
    }
    const oversizedFile = files.find((file) =>
      requestedType === "photo"
        ? file.size > 10 * 1024 * 1024
        : file.size > 50 * 1024 * 1024,
    );
    if (oversizedFile) {
      setError(
        requestedType === "photo"
          ? "Размер фотографии не должен превышать 10 МБ"
          : "Размер видео не должен превышать 50 МБ",
      );
      return;
    }
    setError("");
    setSuccess(false);
    const firstSortOrder =
      Math.max(
        -1,
        ...current.media
          .filter((media) => media.type !== "video_note")
          .map((media) => media.sort_order),
      ) + 1;
    const additions = files.map<MessageMedia>((file, index) => ({
      id: nextDraftMediaID.current--,
      type: requestedType,
      position: "before",
      sort_order: requestedType === "video_note" ? 0 : firstSortOrder + index,
      name: file.name,
      size: file.size,
      file,
      preview_url: URL.createObjectURL(file),
    }));
    setCurrent({ ...current, media: [...current.media, ...additions] });
    setDirty(true);
  }
  function removeMedia(mediaID: number) {
    if (!current) return;
    setError("");
    setSuccess(false);
    const removed = current.media.find((media) => media.id === mediaID);
    if (removed?.preview_url) URL.revokeObjectURL(removed.preview_url);
    if (mediaID > 0) {
      setRemovedMediaIDs((ids) =>
        ids.includes(mediaID) ? ids : [...ids, mediaID],
      );
    }
    const remaining = current.media.filter((media) => media.id !== mediaID);
    const regular = remaining
      .filter((media) => media.type !== "video_note")
      .sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
    const order = new Map(regular.map((media, index) => [media.id, index]));
    setCurrent({
      ...current,
      media: remaining.map((media) => ({
        ...media,
        sort_order: order.get(media.id) ?? media.sort_order,
      })),
    });
    setDirty(true);
  }
  function reorderMedia(mediaID: number, direction: -1 | 1) {
    if (!current) return;
    const ordered = current.media
      .filter((media) => media.type !== "video_note")
      .sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
    const index = ordered.findIndex((media) => media.id === mediaID);
    const target = index + direction;
    if (index < 0 || target < 0 || target >= ordered.length) return;
    [ordered[index], ordered[target]] = [ordered[target], ordered[index]];
    setError("");
    setSuccess(false);
    const order = new Map(
      ordered.map((media, itemIndex) => [media.id, itemIndex]),
    );
    setCurrent({
      ...current,
      media: current.media.map((media) => ({
        ...media,
        sort_order: order.get(media.id) ?? media.sort_order,
      })),
    });
    setDirty(true);
  }
  function select(m: Message) {
    if (
      dirty &&
      !window.confirm("Есть несохранённые изменения. Перейти без сохранения?")
    )
      return;
    if (current) releaseDraftMedia(current.media);
    setCurrent(structuredClone(m));
    setRemovedMediaIDs([]);
    setDirty(false);
    setSuccess(false);
  }
  function button(index: number, patch: Partial<MessageButton>) {
    if (current)
      update({
        buttons: current.buttons.map((b, i) =>
          i === index ? { ...b, ...patch } : b,
        ),
      });
  }
  const orderedRegularMedia = (current?.media || [])
    .filter((media) => media.type !== "video_note")
    .sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
  return (
    <>
      <div className="page-heading">
        <div>
          <Typography variant="h4">Сообщения</Typography>
          <Typography color="text.secondary" sx={{ mt: 1 }}>
            Тексты и кнопки, из которых складывается диалог с клубом.
          </Typography>
        </div>
        <Chip
          label={`${messages.length} шаблонов`}
          size="small"
          variant="outlined"
        />
      </div>
      {error && (
        <Alert
          severity="error"
          sx={{ mb: 2 }}
          action={<Button onClick={() => load(current?.id)}>Обновить</Button>}
        >
          {error}
        </Alert>
      )}
      {success && (
        <Alert severity="success" sx={{ mb: 2 }}>
          Изменения сохранены
        </Alert>
      )}
      {loading ? (
        <CircularProgress />
      ) : current ? (
        <div className="editor-layout">
          <Paper
            variant="outlined"
            sx={{ overflow: "hidden", alignSelf: "start" }}
          >
            <div style={{ padding: 14 }}>
              <TextField
                placeholder="Найти сообщение"
                fullWidth
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                inputProps={{ "aria-label": "Поиск сообщений" }}
              />
            </div>
            <div className="message-list">
              {messages
                .filter((m) =>
                  (m.name + " " + m.key)
                    .toLowerCase()
                    .includes(search.toLowerCase()),
                )
                .map((m) => (
                  <button
                    key={m.id}
                    className={
                      "message-item " + (current.id === m.id ? "selected" : "")
                    }
                    onClick={() => select(m)}
                  >
                    <span>{m.name}</span>
                    <small>{m.key}</small>
                  </button>
                ))}
            </div>
          </Paper>
          <Paper variant="outlined" sx={{ p: 3, alignSelf: "start" }}>
            <Stack spacing={2.5}>
              <Stack
                direction="row"
                justifyContent="space-between"
                alignItems="center"
              >
                <Typography fontWeight={600}>Редактор сообщения</Typography>
                <Chip
                  label={current.key}
                  size="small"
                  sx={{ fontFamily: "monospace", fontSize: 10 }}
                />
              </Stack>
              <TextField
                label="Название"
                value={current.name}
                onChange={(e) => update({ name: e.target.value })}
              />
              {current.key.startsWith("command_") ? (
                <TextField
                  label="Описание команды"
                  value={current.text}
                  onChange={(e) =>
                    update({ text: e.target.value, parse_mode: "" })
                  }
                  multiline
                  minRows={4}
                  helperText={`${current.text.length} / 256 символов`}
                  inputProps={{ maxLength: 256 }}
                />
              ) : (
                <RichTextEditor
                  key={`${current.id}-${current.version}`}
                  value={current.text}
                  parseMode={current.parse_mode}
                  onChange={(text) => update({ text, parse_mode: "HTML" })}
                  maxLength={
                    current.media.some((media) => media.type !== "video_note")
                      ? 1024
                      : 3500
                  }
                />
              )}
              <TemplateVariablesHelp />
              {!current.key.startsWith("command_") && (
                <>
                  <Divider />
                  <Typography fontWeight={600}>Медиа</Typography>
                  <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
                    <Button
                      component="label"
                      variant="outlined"
                      disabled={
                        uploadingMedia ||
                        current.media.some((item) => item.type === "video_note")
                      }
                    >
                      + Загрузить кружок
                      <input
                        hidden
                        type="file"
                        accept="video/mp4,.mp4"
                        onChange={(e) => {
                          const file = e.target.files?.[0];
                          if (file) void uploadMedia([file], "video_note");
                          e.target.value = "";
                        }}
                      />
                    </Button>
                    <Button
                      component="label"
                      variant="outlined"
                      disabled={
                        uploadingMedia ||
                        current.media.filter(
                          (item) => item.type !== "video_note",
                        ).length >= 10
                      }
                    >
                      {uploadingMedia ? "Загружаем…" : "+ Загрузить фото"}
                      <input
                        hidden
                        multiple
                        type="file"
                        accept="image/jpeg,image/png,.jpg,.jpeg,.png"
                        onChange={(e) => {
                          const files = Array.from(e.target.files || []);
                          if (files.length) void uploadMedia(files, "photo");
                          e.target.value = "";
                        }}
                      />
                    </Button>
                    <Button
                      component="label"
                      variant="outlined"
                      disabled={
                        uploadingMedia ||
                        current.media.filter(
                          (item) => item.type !== "video_note",
                        ).length >= 10
                      }
                    >
                      + Загрузить видео
                      <input
                        hidden
                        multiple
                        type="file"
                        accept="video/mp4,.mp4"
                        onChange={(e) => {
                          const files = Array.from(e.target.files || []);
                          if (files.length) void uploadMedia(files, "video");
                          e.target.value = "";
                        }}
                      />
                    </Button>
                  </Stack>
                  <Typography variant="caption" color="text.secondary">
                    До 10 фотографий или видео и один кружок. Фото — JPEG/PNG до
                    10 МБ, видео — MP4 до 50 МБ.
                  </Typography>
                  {[...current.media]
                    .sort((a, b) => {
                      if (a.type === "video_note" && b.type !== "video_note") {
                        return -1;
                      }
                      if (a.type !== "video_note" && b.type === "video_note") {
                        return 1;
                      }
                      return a.sort_order - b.sort_order || a.id - b.id;
                    })
                    .map((media) => (
                      <Paper variant="outlined" sx={{ p: 2 }} key={media.id}>
                        <Stack spacing={1.5}>
                          <Typography variant="body2" fontWeight={600}>
                            {media.type === "video_note"
                              ? "Кружок"
                              : media.type === "photo"
                                ? "Фотография"
                                : "Видео"}
                            : {media.name}
                          </Typography>
                          <Typography variant="caption" color="text.secondary">
                            {(media.size / 1024 / 1024).toFixed(1)} МБ
                          </Typography>
                          {media.type === "video_note" && (
                            <TextField
                              select
                              size="small"
                              label="Расположение кружка"
                              value={media.position}
                              onChange={(event) =>
                                update({
                                  media: current.media.map((item) =>
                                    item.id === media.id
                                      ? {
                                          ...item,
                                          position: event.target.value as
                                            "before" | "after",
                                        }
                                      : item,
                                  ),
                                })
                              }
                            >
                              <MenuItem value="before">До сообщения</MenuItem>
                              <MenuItem value="after">После сообщения</MenuItem>
                            </TextField>
                          )}
                          <Stack
                            direction="row"
                            spacing={1}
                            alignItems="center"
                          >
                            {media.type !== "video_note" && (
                              <>
                                <Button
                                  variant="outlined"
                                  disabled={
                                    uploadingMedia ||
                                    orderedRegularMedia[0]?.id === media.id
                                  }
                                  onClick={() =>
                                    void reorderMedia(media.id, -1)
                                  }
                                >
                                  ←
                                </Button>
                                <Button
                                  variant="outlined"
                                  disabled={
                                    uploadingMedia ||
                                    orderedRegularMedia.at(-1)?.id === media.id
                                  }
                                  onClick={() => void reorderMedia(media.id, 1)}
                                >
                                  →
                                </Button>
                              </>
                            )}
                            <Button
                              color="error"
                              variant="outlined"
                              onClick={() => void removeMedia(media.id)}
                              disabled={uploadingMedia}
                            >
                              Удалить
                            </Button>
                          </Stack>
                        </Stack>
                      </Paper>
                    ))}
                </>
              )}
              <Divider />
              <Typography fontWeight={600}>Кнопки сообщения</Typography>
              {current.buttons.map((b, i) => (
                <Paper variant="outlined" key={i} sx={{ p: 2 }}>
                  <Stack spacing={1.5}>
                    <Typography variant="caption">Кнопка {i + 1}</Typography>
                    <TextField
                      label="Подпись"
                      value={b.label}
                      onChange={(e) => button(i, { label: e.target.value })}
                    />
                    <Stack direction="row" spacing={1}>
                      <TextField
                        select
                        label="Тип"
                        fullWidth
                        value={b.type}
                        onChange={(e) =>
                          button(i, {
                            type: e.target.value as MessageButton["type"],
                            action:
                              e.target.value === "url"
                                ? "https://t.me/your_support"
                                : "club:join",
                          })
                        }
                      >
                        <MenuItem value="callback">Действие</MenuItem>
                        <MenuItem value="url">Ссылка</MenuItem>
                      </TextField>
                      <TextField
                        label="Строка"
                        type="number"
                        sx={{ width: 100 }}
                        value={b.row}
                        onChange={(e) =>
                          button(i, { row: Number(e.target.value) })
                        }
                        inputProps={{ min: 0, max: 20 }}
                      />
                    </Stack>
                    {b.type === "callback" ? (
                      <TextField
                        select
                        label="Действие"
                        value={b.action}
                        onChange={(e) => button(i, { action: e.target.value })}
                      >
                        {actions.map((action) => (
                          <MenuItem value={action} key={action}>
                            {action}
                          </MenuItem>
                        ))}
                      </TextField>
                    ) : (
                      <TextField
                        label="HTTPS URL или переменная ссылки"
                        value={b.action}
                        onChange={(e) => button(i, { action: e.target.value })}
                        helperText="{{payment_url}}, {{invite_url}} или обычная HTTPS-ссылка"
                      />
                    )}
                    <Stack direction="row" spacing={1}>
                      <Button
                        size="small"
                        disabled={i === 0}
                        onClick={() => {
                          const buttons = [...current.buttons];
                          [buttons[i - 1], buttons[i]] = [
                            buttons[i],
                            buttons[i - 1],
                          ];
                          update({ buttons });
                        }}
                      >
                        ↑
                      </Button>
                      <Button
                        size="small"
                        disabled={i === current.buttons.length - 1}
                        onClick={() => {
                          const buttons = [...current.buttons];
                          [buttons[i + 1], buttons[i]] = [
                            buttons[i],
                            buttons[i + 1],
                          ];
                          update({ buttons });
                        }}
                      >
                        ↓
                      </Button>
                      <Button
                        size="small"
                        color="error"
                        onClick={() =>
                          update({
                            buttons: current.buttons.filter(
                              (_, index) => index !== i,
                            ),
                          })
                        }
                      >
                        Удалить
                      </Button>
                    </Stack>
                  </Stack>
                </Paper>
              ))}
              <Button
                variant="outlined"
                onClick={() =>
                  update({
                    buttons: [
                      ...current.buttons,
                      {
                        label: "Подробнее",
                        type: "callback",
                        action: "club:about",
                        row: current.buttons.length,
                        enabled: true,
                      },
                    ],
                  })
                }
              >
                + Добавить кнопку
              </Button>
              <Divider />
              <Stack
                direction="row"
                spacing={1}
                useFlexGap
                flexWrap="wrap"
                alignItems="center"
              >
                <Button
                  variant="contained"
                  onClick={save}
                  disabled={saving || !dirty}
                >
                  {saving ? "Сохраняем…" : "Сохранить изменения"}
                </Button>
                <Button
                  disabled={!dirty || saving}
                  onClick={() => load(current.id)}
                >
                  Сбросить
                </Button>
                {success && (
                  <Typography
                    variant="body2"
                    color="success.main"
                    fontWeight={600}
                    role="status"
                    aria-live="polite"
                  >
                    ✓ Изменения сохранены
                  </Typography>
                )}
              </Stack>
            </Stack>
          </Paper>
          <MessagePreview message={current} />
        </div>
      ) : (
        <Paper className="empty">
          Сообщений пока нет. Выполните миграции базы данных.
        </Paper>
      )}
    </>
  );
}
