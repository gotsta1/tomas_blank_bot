import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Collapse,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControlLabel,
  IconButton,
  LinearProgress,
  MenuItem,
  Paper,
  Stack,
  Switch,
  TextField,
  Typography,
} from "@mui/material";
import {
  APIError,
  api,
  messagePayload,
  uploadMessageMedia,
  type Button as MsgButton,
  type Message,
  type MessageMedia,
  type Step,
} from "./api";
import { RichTextEditor } from "./richTextEditor";
import { actions, MessagePreview, TemplateVariablesHelp } from "./messages";

type Draft = { step: Step; message: Message };
type Campaign = {
  id: number;
  name: string;
  audience_status: "all" | "new" | "active" | "expired";
  enabled: boolean;
  block_count: number;
  run_status: "draft" | "scheduled" | "sending" | "completed";
  scheduled_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  recipient_count: number;
  completed_count: number;
  failed_count: number;
  sent_count: number;
  message_count: number;
  run_count: number;
};
type MailingRun = {
  id: number;
  audience_status: Campaign["audience_status"];
  status: "scheduled" | "sending" | "completed";
  scheduled_at: string;
  started_at: string | null;
  completed_at: string | null;
  recipient_count: number;
  completed_count: number;
  failed_count: number;
};
const audiences: Record<Campaign["audience_status"], string> = {
  all: "Все пользователи",
  new: "Новые",
  active: "Активная подписка",
  expired: "Подписка истекла",
};
const moscowToday = () => {
  const parts = new Intl.DateTimeFormat("en-GB", { timeZone: "Europe/Moscow", year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(new Date());
  const value = (part: string) => parts.find((item) => item.type === part)?.value ?? "";
  return `${value("year")}-${value("month")}-${value("day")}`;
};
const calendarDate = (date: Date) => date.toISOString().slice(0, 10);
const prettyDate = (date: string) => new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" }).format(new Date(`${date}T12:00:00Z`));
const prettyMonth = (year: number, month: number) => new Intl.DateTimeFormat("ru-RU", { month: "long", year: "numeric", timeZone: "UTC" }).format(new Date(Date.UTC(year, month, 1)));
const moscowDateTime = (value: string) => {
  const parts = new Intl.DateTimeFormat("en-GB", { timeZone: "Europe/Moscow", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(new Date(value));
  const part = (type: string) => parts.find((item) => item.type === type)?.value ?? "";
  return { day: `${part("year")}-${part("month")}-${part("day")}`, time: `${part("hour")}:${part("minute")}` };
};
const blankMessage = (): Message => ({
  id: 0,
  key: "",
  name: "",
  text: "",
  parse_mode: "HTML",
  buttons: [],
  version: 1,
  media: [],
});
function MailingProgress({ campaign }: { campaign: Campaign }) {
  const [visible, setVisible] = useState(campaign.run_status === "sending");
  useEffect(() => {
    if (campaign.run_status === "sending") {
      setVisible(true);
      return;
    }
    if (campaign.run_status === "completed" && visible) {
      const timer = window.setTimeout(() => setVisible(false), 1700);
      return () => window.clearTimeout(timer);
    }
    setVisible(false);
  }, [campaign.run_status, visible]);
  if (!visible) return null;
  const totalMessages = campaign.recipient_count * campaign.message_count;
  const delivered = Math.min(campaign.sent_count, totalMessages);
  const percentage = campaign.run_status === "completed" ? 100 : totalMessages > 0 ? Math.round(delivered / totalMessages * 100) : 0;
  return (
    <Box sx={{ width: "100%", maxWidth: 560, mt: 1.5 }}>
      <Stack direction="row" justifyContent="space-between" gap={2} sx={{ mb: 0.75 }}>
        <Typography variant="body2" fontWeight={600}>Отправлено {delivered} из {totalMessages} сообщений</Typography>
        <Typography variant="body2" color="text.secondary">{percentage}%</Typography>
      </Stack>
      <LinearProgress variant="determinate" value={percentage} aria-label="Ход рассылки" sx={{ height: 8, borderRadius: 4, "& .MuiLinearProgress-bar": { transition: "transform 900ms ease-in-out" } }} />
      <Typography variant="body2" color="text.secondary" sx={{ mt: 0.75 }}>
        {campaign.completed_count} из {campaign.recipient_count} получателей завершили цепочку
        {campaign.failed_count > 0 ? ` · ошибок: ${campaign.failed_count}` : ""}
      </Typography>
    </Box>
  );
}
export function Broadcasts({ mode = "auto" }: { mode?: "auto" | "manual" }) {
  const manual = mode === "manual";
  const base = manual ? "/mailings" : "/broadcasts";
  const section = manual ? "Рассылки" : "Авторассылки";
  const { id: campaignParam } = useParams();
  const campaignID = Number(campaignParam || 0);
  const navigate = useNavigate();
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [runs, setRuns] = useState<MailingRun[]>([]);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [campaignDraft, setCampaignDraft] = useState<Campaign | null>(null);
  const [steps, setSteps] = useState<Step[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [delayDraft, setDelayDraft] = useState<Step | null>(null);
  const [delayAmount, setDelayAmount] = useState(1);
  const [delayUnit, setDelayUnit] = useState<
    "seconds" | "minutes" | "hours" | "days"
  >("hours");
  const [draggedID, setDraggedID] = useState<number | null>(null);
  const [removed, setRemoved] = useState<number[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const pendingCampaignIDs = useRef(new Set<number>());
  const [pendingIDs, setPendingIDs] = useState<Set<number>>(new Set());
  const [campaignSaved, setCampaignSaved] = useState(false);
  const [messageSaved, setMessageSaved] = useState(false);
  const [sendDialog, setSendDialog] = useState(false);
  const [sendWhen, setSendWhen] = useState<"now" | "later">("now");
  const [editingSchedule, setEditingSchedule] = useState(false);
  const [scheduledDates, setScheduledDates] = useState<Record<string, string>>({});
  const [scheduleRunIDs, setScheduleRunIDs] = useState<Record<string, number>>({});
  const [expectedRunIDs, setExpectedRunIDs] = useState<number[]>([]);
  const [focusedDate, setFocusedDate] = useState("");
  const [calendarMonth, setCalendarMonth] = useState(() => moscowToday().slice(0, 7));
  const nextID = useRef(-1);
  const loadRequest = useRef(0);
  async function showMutationError(error: unknown, partial = false) {
    const message = (error as Error).message;
    if (error instanceof APIError && error.status === 409 && message.startsWith("Рассылка уже отправляется")) {
      if (campaignID) {
        try {
          const current = await api<Campaign>(`${base}/${campaignID}`);
          setCampaign(current);
        } catch {
          // Preserve the server's conflict message even if status refresh fails.
        }
      }
      setError(message + (partial ? " Часть изменений могла сохраниться до начала отправки. Проверьте шаг после её завершения." : ""));
      return;
    }
    setError(message + (partial ? " Шаг выключен до повторного сохранения." : ""));
  }
  async function load() {
    const request = ++loadRequest.current;
    try {
      if (campaignID) {
        const [c, s, m, history] = await Promise.all([
          api<Campaign>(`${base}/${campaignID}`),
          api<Step[]>(`${base}/${campaignID}/steps`),
          api<Message[]>("/messages"),
          manual ? api<MailingRun[]>(`${base}/${campaignID}/runs`) : Promise.resolve([]),
        ]);
        if (request !== loadRequest.current) return;
        setCampaign(c);
        setCampaignDraft(c);
        setSteps(s);
        setMessages(m);
        setRuns(history);
      } else {
        const items = await api<Campaign[]>(base);
        if (request !== loadRequest.current) return;
        setCampaigns(items);
      }
      setError("");
    } catch (e) {
      if (request === loadRequest.current) setError((e as Error).message);
    } finally {
      if (request === loadRequest.current) setLoading(false);
    }
  }
  useEffect(() => {
    setLoading(true);
    setError("");
    setHistoryOpen(false);
    setCampaignDraft(null);
    setCampaignSaved(false);
    void load();
  }, [campaignID, mode]);
  useEffect(() => {
    if (!manual) return;
    const timer = window.setInterval(() => {
      if (campaignID) {
        void api<Campaign>(`${base}/${campaignID}`).then((current) => {
          setCampaign(current);
          setCampaignDraft((previous) =>
            previous && (previous.run_status === "scheduled" || previous.run_status === "sending") && current.run_status === "completed"
              ? current : previous,
          );
        }).catch(() => {});
        void api<MailingRun[]>(`${base}/${campaignID}/runs`).then(setRuns).catch(() => {});
      } else {
        void api<Campaign[]>(base).then(setCampaigns).catch(() => {});
      }
    }, 5000);
    return () => window.clearInterval(timer);
  }, [campaignID, mode]);
  async function saveCampaign() {
    if (!campaignDraft?.name.trim()) {
      setError("Укажите название рассылки");
      return;
    }
    setSaving(true);
    setCampaignSaved(false);
    setError("");
    try {
      await api<{ id: number }>(
        `${base}${campaignDraft.id ? `/${campaignDraft.id}` : ""}`,
        {
          method: campaignDraft.id ? "PUT" : "POST",
          body: JSON.stringify(campaignDraft),
        },
      );
      if (!campaignDraft.id) {
        setCampaignDraft(null);
      }
      await load();
      if (campaignDraft.id) setCampaignSaved(true);
    } catch (e) {
      await showMutationError(e);
    } finally {
      setSaving(false);
    }
  }
  async function deleteCampaign(item: Campaign) {
    if (
      !window.confirm(
        `Удалить рассылку «${item.name}» со всеми её сообщениями?`,
      )
    )
      return;
    try {
      await api(`${base}/${item.id}`, { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function toggleCampaign(item: Campaign, enabled: boolean) {
    if (pendingCampaignIDs.current.has(item.id)) return;
    pendingCampaignIDs.current.add(item.id);
    setPendingIDs(new Set(pendingCampaignIDs.current));
    setError("");
    setCampaigns((current) =>
      current.map((campaign) =>
        campaign.id === item.id ? { ...campaign, enabled } : campaign,
      ),
    );
    try {
      await api(`${base}/${item.id}`, {
        method: "PUT",
        body: JSON.stringify({ ...item, enabled }),
      });
    } catch (e) {
      setCampaigns((current) =>
        current.map((campaign) => (campaign.id === item.id ? item : campaign)),
      );
      setError((e as Error).message);
    } finally {
      pendingCampaignIDs.current.delete(item.id);
      setPendingIDs(new Set(pendingCampaignIDs.current));
    }
  }
  async function launchMailing() {
    if (!campaign || (!editingSchedule && campaign.run_status !== "draft" && campaign.run_status !== "completed" && !(campaign.run_status === "scheduled" && sendWhen === "now"))) return;
    let scheduled_times: string[] = [];
    if (sendWhen === "later") {
      if (!Object.keys(scheduledDates).length && !editingSchedule) {
        setError("Выберите хотя бы одну дату в календаре");
        return;
      }
      for (const [day, time] of Object.entries(scheduledDates).sort(([a], [b]) => a.localeCompare(b))) {
        const date = new Date(`${day}T${time}:00+03:00`);
        if (!/^\d\d:\d\d$/.test(time) || Number.isNaN(date.getTime()) || date <= new Date()) {
          setError(`Укажите будущее время по Москве для ${prettyDate(day)}`);
          setFocusedDate(day);
          return;
        }
        scheduled_times.push(date.toISOString());
      }
    }
    setSaving(true);
    setError("");
    try {
      if (editingSchedule) {
        await api(`${base}/${campaign.id}/schedule`, {
          method: "PUT",
          body: JSON.stringify({ expected_run_ids: expectedRunIDs, runs: Object.entries(scheduledDates).map(([day, time]) => ({ id: scheduleRunIDs[day] ?? 0, scheduled_at: new Date(`${day}T${time}:00+03:00`).toISOString() })) }),
        });
      } else {
        await api(`${base}/${campaign.id}/send`, {
          method: "POST",
          body: JSON.stringify(sendWhen === "now" ? { scheduled_at: null } : { scheduled_times }),
        });
      }
      setSendDialog(false);
      await load();
    } catch (e) {
      await showMutationError(e);
    } finally {
      setSaving(false);
    }
  }
  const formatMoscow = (value: string | null) =>
    value
      ? new Intl.DateTimeFormat("ru-RU", {
          timeZone: "Europe/Moscow", dateStyle: "short", timeStyle: "short",
        }).format(new Date(value)) + " МСК"
      : "—";
  const [calendarYear, calendarMonthNumber] = calendarMonth.split("-").map(Number);
  const firstWeekday = (new Date(Date.UTC(calendarYear, calendarMonthNumber - 1, 1)).getUTCDay() + 6) % 7;
  const calendarDays = new Date(Date.UTC(calendarYear, calendarMonthNumber, 0)).getUTCDate();
  const pickedDates = Object.entries(scheduledDates).sort(([a], [b]) => a.localeCompare(b));
  const completedRuns = runs.filter((run) => run.status === "completed");
  function moveCalendar(offset: number) {
    const next = new Date(Date.UTC(calendarYear, calendarMonthNumber - 1 + offset, 1));
    setCalendarMonth(calendarDate(next).slice(0, 7));
  }
  function openScheduleEditor() {
    const pending = runs.filter((run) => run.status === "scheduled" && new Date(run.scheduled_at) > new Date());
    const dates: Record<string, string> = {};
    const ids: Record<string, number> = {};
    for (const run of pending) {
      const { day, time } = moscowDateTime(run.scheduled_at);
      dates[day] = time;
      ids[day] = run.id;
    }
    setEditingSchedule(true);
    setSendWhen("later");
    setScheduledDates(dates);
    setScheduleRunIDs(ids);
    setExpectedRunIDs(pending.map((run) => run.id));
    setFocusedDate("");
    setCalendarMonth(pending.length ? moscowDateTime(pending[pending.length - 1].scheduled_at).day.slice(0, 7) : moscowToday().slice(0, 7));
    setError("");
    setSendDialog(true);
  }
  function openSendDialog() {
    setEditingSchedule(false);
    setSendWhen("now");
    setScheduledDates({});
    setScheduleRunIDs({});
    setExpectedRunIDs([]);
    setFocusedDate("");
    setCalendarMonth(moscowToday().slice(0, 7));
    setError("");
    setSendDialog(true);
  }
  function close() {
    draft?.message.media.forEach((m) => {
      if (m.preview_url) URL.revokeObjectURL(m.preview_url);
    });
    setDraft(null);
    setRemoved([]);
    setMessageSaved(false);
    setError("");
  }
  function updateMessage(patch: Partial<Message>) {
    setMessageSaved(false);
    setDraft((d) => d && { ...d, message: { ...d.message, ...patch } });
  }
  function updateStep(patch: Partial<Step>) {
    setMessageSaved(false);
    setDraft((d) => d && { ...d, step: { ...d.step, ...patch } });
  }
  function edit(step: Step) {
    const m = messages.find((item) => item.id === step.message_id);
    if (!m) {
      setError("Сообщение шага не найдено");
      return;
    }
    if (!m.key.startsWith("broadcast_step_")) {
      setError(
        "Этот старый шаг ссылается на общее сообщение. Удалите его и создайте новый шаг.",
      );
      return;
    }
    setDraft({
      step: { ...step },
      message: {
        ...m,
        buttons: m.buttons.map((b) => ({ ...b })),
        media: m.media.map((item) => ({ ...item })),
      },
    });
    setRemoved([]);
    setMessageSaved(false);
    setError("");
  }
  function addMedia(files: File[], type: MessageMedia["type"]) {
    if (!draft || !files.length) return;
    const media = draft.message.media;
    if (type === "video_note" && media.some((m) => m.type === type)) {
      setError("Можно прикрепить только один кружок");
      return;
    }
    if (
      type !== "video_note" &&
      media.filter((m) => m.type !== "video_note").length + files.length > 10
    ) {
      setError("Можно прикрепить не более 10 фото и видео");
      return;
    }
    if (
      files.some((f) =>
        type === "photo"
          ? !["image/jpeg", "image/png"].includes(f.type) ||
            f.size > 10 * 1024 * 1024
          : f.type !== "video/mp4" || f.size > 50 * 1024 * 1024,
      )
    ) {
      setError("Фото: JPEG/PNG до 10 МБ. Видео и кружок: MP4 до 50 МБ.");
      return;
    }
    const order =
      Math.max(
        -1,
        ...media
          .filter((m) => m.type !== "video_note")
          .map((m) => m.sort_order),
      ) + 1;
    updateMessage({
      media: [
        ...media,
        ...files.map((file, i): MessageMedia => ({
          id: nextID.current--,
          type,
          position: "before",
          sort_order: type === "video_note" ? 0 : order + i,
          name: file.name,
          size: file.size,
          file,
          preview_url: URL.createObjectURL(file),
        })),
      ],
    });
    setError("");
  }
  function removeMedia(item: MessageMedia) {
    if (!draft) return;
    if (item.preview_url) URL.revokeObjectURL(item.preview_url);
    if (item.id > 0) setRemoved((ids) => [...ids, item.id]);
    const rest = draft.message.media.filter((m) => m.id !== item.id);
    const order = new Map(
      rest
        .filter((m) => m.type !== "video_note")
        .sort((a, b) => a.sort_order - b.sort_order)
        .map((m, i) => [m.id, i]),
    );
    updateMessage({
      media: rest.map((m) => ({
        ...m,
        sort_order: order.get(m.id) ?? m.sort_order,
      })),
    });
  }
  function moveMedia(id: number, by: -1 | 1) {
    if (!draft) return;
    const regular = draft.message.media
      .filter((m) => m.type !== "video_note")
      .sort((a, b) => a.sort_order - b.sort_order);
    const i = regular.findIndex((m) => m.id === id);
    if (i < 0 || i + by < 0 || i + by >= regular.length) return;
    [regular[i], regular[i + by]] = [regular[i + by], regular[i]];
    const order = new Map(regular.map((m, index) => [m.id, index]));
    updateMessage({
      media: draft.message.media.map((m) => ({
        ...m,
        sort_order: order.get(m.id) ?? m.sort_order,
      })),
    });
  }
  function changeButton(i: number, patch: Partial<MsgButton>) {
    if (draft)
      updateMessage({
        buttons: draft.message.buttons.map((b, j) =>
          i === j ? { ...b, ...patch } : b,
        ),
      });
  }
  function openDelay(step?: Step) {
    const current = step || {
      id: 0,
      kind: "delay" as const,
      message_id: 0,
      position: steps.length + 1,
      delay_seconds: 3600,
      enabled: true,
    };
    const unit =
      current.delay_seconds % 86400 === 0
        ? "days"
        : current.delay_seconds % 3600 === 0
          ? "hours"
          : current.delay_seconds % 60 === 0
            ? "minutes"
            : "seconds";
    setDelayUnit(unit);
    setDelayAmount(
      current.delay_seconds /
        (unit === "days"
          ? 86400
          : unit === "hours"
            ? 3600
            : unit === "minutes"
              ? 60
              : 1),
    );
    setDelayDraft(current);
    setError("");
  }
  async function saveDelay() {
    if (!delayDraft) return;
    const seconds =
      delayAmount *
      (delayUnit === "days"
        ? 86400
        : delayUnit === "hours"
          ? 3600
          : delayUnit === "minutes"
            ? 60
            : 1);
    if (!Number.isInteger(delayAmount) || seconds < 1 || seconds > 31536000) {
      setError("Укажите целое число от одной секунды до одного года");
      return;
    }
    setSaving(true);
    try {
      await api(
        `${base}/${campaignID}/steps${delayDraft.id ? `/${delayDraft.id}` : ""}`,
        {
          method: delayDraft.id ? "PUT" : "POST",
          body: JSON.stringify({
            ...delayDraft,
            kind: "delay",
            delay_seconds: seconds,
          }),
        },
      );
      setDelayDraft(null);
      await load();
    } catch (e) {
      await showMutationError(e);
    } finally {
      setSaving(false);
    }
  }
  async function moveBlock(fromID: number, toID: number) {
    if (fromID === toID) return;
    const reordered = [...steps];
    const from = reordered.findIndex((item) => item.id === fromID);
    const to = reordered.findIndex((item) => item.id === toID);
    if (from < 0 || to < 0) return;
    reordered.splice(to, 0, reordered.splice(from, 1)[0]);
    setSteps(reordered);
    try {
      await api(`${base}/${campaignID}/steps`, {
        method: "PUT",
        body: JSON.stringify({ order: reordered.map((item) => item.id) }),
      });
    } catch (e) {
      await load();
      await showMutationError(e);
    }
  }
  async function save() {
    if (!draft) return;
    setMessageSaved(false);
    const { step, message } = draft;
    if (!message.name.trim() || !message.text.trim()) {
      setError("Укажите название и текст сообщения");
      return;
    }
    if (
      message.media.some((m) => m.type !== "video_note") &&
      Array.from(message.text).length > 1024
    ) {
      setError("С медиа текст должен быть не длиннее 1024 символов");
      return;
    }
    setSaving(true);
    setError("");
    let persisted: { id: number; message_id: number } | null = null;
    try {
      // Keep the step disabled while its media is being changed.
      const result = await api<{ id: number; message_id: number }>(
        `${base}/${campaignID}/steps${step.id ? `/${step.id}` : ""}`,
        {
          method: step.id ? "PUT" : "POST",
          body: JSON.stringify({
            ...step,
            enabled: false,
            message: messagePayload(message),
          }),
        },
      );
      persisted = result;
      setDraft(
        (current) =>
          current && {
            ...current,
            step: {
              ...current.step,
              id: result.id,
              message_id: result.message_id,
            },
            message: { ...current.message, id: result.message_id },
          },
      );
      const path = `/messages/${result.message_id}/media`;
      for (const id of removed) {
        await api(`${path}/${id}`, { method: "DELETE" });
        setRemoved((ids) => ids.filter((item) => item !== id));
      }
      const ordered = [...message.media].sort(
        (a, b) => a.sort_order - b.sort_order || a.id - b.id,
      );
      const uploaded = new Map<number, number>();
      for (const m of ordered) {
        if (m.id > 0 || !m.file) continue;
        const response = await uploadMessageMedia(result.message_id, m, m.file);
        uploaded.set(m.id, response.media.id);
        setDraft(
          (current) =>
            current && {
              ...current,
              message: {
                ...current.message,
                media: current.message.media.map((item) =>
                  item.id === m.id
                    ? { ...item, id: response.media.id, file: undefined }
                    : item,
                ),
              },
            },
        );
      }
      const order = ordered
        .filter((m) => m.type !== "video_note")
        .map((m) => (m.id > 0 ? m.id : uploaded.get(m.id)))
        .filter((id): id is number => id !== undefined);
      if (order.length)
        await api(path, { method: "PUT", body: JSON.stringify({ order }) });
      const note = ordered.find((m) => m.type === "video_note" && m.id > 0);
      if (note)
        await api(`${path}/${note.id}`, {
          method: "PUT",
          body: JSON.stringify({ position: note.position }),
        });
      const fresh = await api<Message>(`/messages/${result.message_id}`);
      await api(`${base}/${campaignID}/steps/${result.id}`, {
        method: "PUT",
        body: JSON.stringify({ ...step, message: messagePayload(fresh) }),
      });
      const saved = await api<Message>(`/messages/${result.message_id}`);
      message.media.forEach((m) => {
        if (m.preview_url) URL.revokeObjectURL(m.preview_url);
      });
      setDraft(
        (current) =>
          current && {
            step: { ...step, id: result.id, message_id: result.message_id },
            message: saved,
          },
      );
      setRemoved([]);
      await load();
      setMessageSaved(true);
    } catch (e) {
      if (persisted) {
        try {
          const latest = await api<Message>(
            `/messages/${persisted.message_id}`,
          );
          setDraft(
            (current) =>
              current && {
                ...current,
                message: { ...current.message, version: latest.version },
              },
          );
        } catch {
          // Keep the visible draft so the user can inspect it before reloading.
        }
      }
      await showMutationError(e, persisted !== null);
    } finally {
      setSaving(false);
    }
  }
  async function deleteStep(step: Step) {
    if (
      !window.confirm(
        step.kind === "delay"
          ? "Удалить задержку?"
          : "Удалить сообщение из рассылки?",
      )
    )
      return;
    try {
      await api(`${base}/${campaignID}/steps/${step.id}`, {
        method: "DELETE",
      });
      await load();
    } catch (e) {
      await showMutationError(e);
    }
  }
  const message = draft?.message;
  const canEdit = !manual || campaign?.run_status !== "sending";
  const regular =
    message?.media
      .filter((m) => m.type !== "video_note")
      .sort((a, b) => a.sort_order - b.sort_order) || [];
  function delayLabel(seconds: number) {
    if (seconds % 86400 === 0) return `${seconds / 86400} дн.`;
    if (seconds % 3600 === 0) return `${seconds / 3600} ч`;
    if (seconds % 60 === 0) return `${seconds / 60} мин`;
    return `${seconds} сек`;
  }
  if (
    campaignID &&
    (loading || campaign?.id !== campaignID || campaignDraft?.id !== campaignID)
  ) {
    return (
      <Box sx={{ display: "flex", justifyContent: "center", py: 8 }}>
        {error ? <Alert severity="error">{error}</Alert> : <CircularProgress />}
      </Box>
    );
  }
  if (!campaignID)
    return (
      <>
        <div className="page-heading">
          <div>
            <Typography variant="h4">{section}</Typography>
            <Typography color="text.secondary" sx={{ mt: 1 }}>
              Выберите рассылку или создайте новую для нужной группы
              пользователей.
            </Typography>
          </div>
          <Button
            variant="contained"
            onClick={() => {
              setCampaignDraft({
                id: 0,
                name: "",
                audience_status: "new",
                enabled: false,
                block_count: 0,
                run_status: "draft",
                scheduled_at: null,
                started_at: null,
                completed_at: null,
                recipient_count: 0,
                completed_count: 0,
                failed_count: 0,
                sent_count: 0,
                message_count: 0,
                run_count: 0,
              });
              setError("");
            }}
          >
            + Создать {manual ? "рассылку" : "авторассылку"}
          </Button>
        </div>
        {!manual && <Alert severity="info" sx={{ mb: 3 }}>
          Статус пользователя пересчитывается перед отправкой. Если аудитории
          нескольких включённых рассылок пересекаются, пользователь может
          получать сообщения из каждой.
        </Alert>}
        {error && !campaignDraft && (
          <Alert severity="error" sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}
        {loading ? (
          <CircularProgress />
        ) : campaigns.length === 0 ? (
          <Paper variant="outlined" className="empty">
            <Typography variant="h6">Рассылок пока нет</Typography>
            <Typography sx={{ mt: 1 }}>
              Создайте рассылку, затем добавьте сообщения и задержки.
            </Typography>
          </Paper>
        ) : (
          <Stack spacing={2}>
            {campaigns.map((item) => (
              <Paper variant="outlined" key={item.id} sx={{ p: 3 }}>
                <Stack
                  direction={{ xs: "column", sm: "row" }}
                  justifyContent="space-between"
                  alignItems={{ sm: "center" }}
                  gap={2}
                >
                  <div>
                    <Typography variant="h6">{item.name}</Typography>
                    <Typography variant="body2" color="text.secondary">
                      {audiences[item.audience_status]} · {item.block_count}{" "}
                      блоков
                    </Typography>
                    {manual && <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                      {item.run_status === "draft" ? "Черновик" : item.run_status === "scheduled" ? `Запланирована: ${formatMoscow(item.scheduled_at)}` : item.run_status === "sending" ? `Отправляется с ${formatMoscow(item.started_at)} · ${item.completed_count}/${item.recipient_count} получателей` : `Последняя отправка завершена: ${formatMoscow(item.completed_at)} · ${item.completed_count} доставлено, ${item.failed_count} ошибок`}
                    </Typography>}
                    {manual && <MailingProgress campaign={item} />}
                    {manual && item.run_count > 0 && <Typography variant="caption" color="text.secondary">Запусков: {item.run_count}</Typography>}
                  </div>
                  {!manual && <Stack direction="row" spacing={1} alignItems="center" aria-busy={pendingIDs.has(item.id)}>
                    <FormControlLabel
                      sx={{ m: 0 }}
                      control={
                        <Switch
                          checked={item.enabled}
                          onChange={(event) =>
                            void toggleCampaign(item, event.target.checked)
                          }
                          inputProps={{
                            "aria-label": `${item.enabled ? "Выключить" : "Включить"} рассылку «${item.name}»`,
                          }}
                        />
                      }
                      label={<Box component="span" sx={{ display: "inline-block", minWidth: 100 }}>{item.enabled ? "Включена" : "Выключена"}</Box>}
                    />
                    <Box sx={{ width: 18, height: 18, flexShrink: 0 }}>
                      {pendingIDs.has(item.id) && <CircularProgress size={18} />}
                    </Box>
                  </Stack>}
                </Stack>
                <Stack direction="row" spacing={1} sx={{ mt: 2 }}>
                  <Button
                    variant="outlined"
                    onClick={() => navigate(`${base}/${item.id}`)}
                  >
                    Открыть рассылку
                  </Button>
                  {(!manual || item.run_status === "draft" || item.run_status === "completed") && <Button
                    color="error"
                    onClick={() => void deleteCampaign(item)}
                  >
                    Удалить
                  </Button>}
                </Stack>
              </Paper>
            ))}
          </Stack>
        )}
        <Dialog
          open={campaignDraft?.id === 0}
          onClose={() => !saving && setCampaignDraft(null)}
          fullWidth
          maxWidth="sm"
        >
          <DialogTitle>Новая рассылка</DialogTitle>
          <DialogContent>
            <Stack spacing={2} sx={{ pt: 1 }}>
              {error && <Alert severity="error">{error}</Alert>}
              <TextField
                label="Название"
                value={campaignDraft?.name || ""}
                onChange={(e) =>
                  setCampaignDraft((d) => d && { ...d, name: e.target.value })
                }
              />
              <TextField
                select
                label="Кому отправлять"
                value={campaignDraft?.audience_status || "new"}
                onChange={(e) =>
                  setCampaignDraft(
                    (d) =>
                      d && {
                        ...d,
                        audience_status: e.target
                          .value as Campaign["audience_status"],
                      },
                  )
                }
              >
                {Object.entries(audiences).map(([value, label]) => (
                  <MenuItem key={value} value={value}>
                    {label}
                  </MenuItem>
                ))}
              </TextField>
              <Typography variant="body2" color="text.secondary">
                {manual ? "После создания добавьте блоки и запустите отправку или назначьте время." : "Новая авторассылка будет выключена, пока вы не включите её в настройках."}
              </Typography>
            </Stack>
          </DialogContent>
          <DialogActions>
            <Button disabled={saving} onClick={() => setCampaignDraft(null)}>
              Отмена
            </Button>
            <Button
              variant="contained"
              disabled={saving}
              onClick={() => void saveCampaign()}
            >
              {saving ? "Создаём…" : "Создать"}
            </Button>
          </DialogActions>
        </Dialog>
      </>
    );
  return (
    <>
      <div className="page-heading">
        <div>
          <Button onClick={() => navigate(base)} sx={{ mb: 1 }}>
            ← Все {manual ? "рассылки" : "авторассылки"}
          </Button>
          <Typography variant="h4">{campaign?.name || "Рассылка"}</Typography>
          <Typography color="text.secondary" sx={{ mt: 1 }}>
            Сообщения и задержки отправляются в указанном ниже порядке.
          </Typography>
        </div>
        {canEdit && <Stack direction="row" spacing={1}>
          <Button variant="outlined" onClick={() => openDelay()}>
            + Добавить задержку
          </Button>
          <Button
            variant="contained"
            onClick={() => {
              setDraft({
                step: {
                  id: 0,
                  kind: "message",
                  message_id: 0,
                  position: steps.length + 1,
                  delay_seconds: 0,
                  enabled: true,
                },
                message: blankMessage(),
              });
              setRemoved([]);
              setMessageSaved(false);
              setError("");
            }}
          >
            + Добавить сообщение
          </Button>
        </Stack>}
      </div>
      {manual && campaign?.run_status === "sending" && (
        <Alert severity="info" sx={{ mb: 3 }}>
          Рассылка сейчас отправляется. Сообщения и задержки нельзя редактировать, пока отправка не завершится.
        </Alert>
      )}
      {campaignDraft && (
        <Paper variant="outlined" sx={{ p: 3, mb: 3 }}>
          <Stack spacing={2}>
            <Typography fontWeight={600}>Настройки рассылки</Typography>
            <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
              <TextField
                label="Название"
                fullWidth
                value={campaignDraft.name}
                onChange={(e) => {
                  setCampaignSaved(false);
                  setCampaignDraft({ ...campaignDraft, name: e.target.value });
                }}
              />
              <TextField
                select
                label="Кому отправлять"
                fullWidth
                disabled={manual && campaign?.run_status === "sending"}
                value={campaignDraft.audience_status}
                onChange={(e) => {
                  setCampaignSaved(false);
                  setCampaignDraft({
                    ...campaignDraft,
                    audience_status: e.target
                      .value as Campaign["audience_status"],
                  });
                }}
              >
                {Object.entries(audiences).map(([value, label]) => (
                  <MenuItem key={value} value={value}>
                    {label}
                  </MenuItem>
                ))}
              </TextField>
            </Stack>
            {manual && campaign?.run_status === "scheduled" && <Typography variant="body2" color="text.secondary">До начала отправки можно изменить аудиторию, сообщения, задержки, название и расписание.</Typography>}
            {error && !draft && !delayDraft && <Alert severity="error">{error}</Alert>}
            <Stack
              direction="row"
              spacing={2}
              alignItems="center"
              useFlexGap
              flexWrap="wrap"
            >
              {!manual && <FormControlLabel
                control={
                  <Switch
                    checked={campaignDraft.enabled}
                    onChange={(e) => {
                      setCampaignSaved(false);
                      setCampaignDraft({
                        ...campaignDraft,
                        enabled: e.target.checked,
                      });
                    }}
                  />
                }
                label="Рассылка включена"
              />}
              <Button
                variant="contained"
                disabled={saving}
                onClick={() => void saveCampaign()}
              >
                {saving ? "Сохраняем…" : "Сохранить настройки"}
              </Button>
              {campaignSaved && (
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
      )}
      {manual && campaign && (
        <Paper variant="outlined" sx={{ p: 3, mb: 3 }}>
          <Stack spacing={1.5} alignItems="flex-start">
            <Typography fontWeight={600}>Отправка</Typography>
            <Typography color="text.secondary">
              {campaign.run_status === "draft" ? "Черновик. Получатели будут выбраны по фильтру в момент запуска." :
                campaign.run_status === "scheduled" ? `Запланирована на ${formatMoscow(campaign.scheduled_at)}.` :
                campaign.run_status === "sending" ? `Отправляется с ${formatMoscow(campaign.started_at)}.` :
                `Последняя отправка завершена ${formatMoscow(campaign.completed_at)}. Получателей: ${campaign.recipient_count}, завершено: ${campaign.completed_count}, ошибок: ${campaign.failed_count}.`}
            </Typography>
            <MailingProgress campaign={campaign} />
            {(campaign.run_status === "draft" || campaign.run_status === "completed") && <Button
              variant="contained"
              disabled={!steps.some((step) => step.kind === "message" && step.enabled) || saving || campaignDraft?.name !== campaign.name || campaignDraft?.audience_status !== campaign.audience_status}
              onClick={openSendDialog}
            >{campaign.run_status === "completed" ? "Отправить повторно / запланировать" : "Отправить / запланировать"}</Button>}
            {campaign.run_status === "scheduled" && <Button variant="contained"
              disabled={saving || campaignDraft?.name !== campaign.name}
              onClick={openSendDialog}>Отправить сейчас</Button>}
            {(campaign.run_status === "scheduled" || campaign.run_status === "sending") && <Button variant="outlined" disabled={saving} onClick={openScheduleEditor}>Изменить расписание</Button>}
            <Button variant="text" aria-expanded={historyOpen} aria-controls="mailing-run-history" onClick={() => setHistoryOpen((open) => !open)}>
              {historyOpen ? "Скрыть историю запусков" : "История запусков"}{completedRuns.length ? ` (${completedRuns.length})` : ""}
            </Button>
            {canEdit && (campaignDraft?.name !== campaign.name || campaignDraft?.audience_status !== campaign.audience_status) && <Typography variant="body2" color="text.secondary">Сначала сохраните настройки.</Typography>}
          </Stack>
        </Paper>
      )}
      {manual && <Collapse in={historyOpen} unmountOnExit>
        <Paper id="mailing-run-history" variant="outlined" sx={{ p: 3, mb: 3 }}>
          <Typography fontWeight={600} sx={{ mb: 2 }}>История запусков</Typography>
          {completedRuns.length ? <Stack spacing={1.5}>
            {completedRuns.map((run, index) => <Box key={run.id}>
              <Typography fontWeight={600}>Отправка {completedRuns.length - index} · завершена</Typography>
              <Typography variant="body2" color="text.secondary">
                {audiences[run.audience_status]} · назначен: {formatMoscow(run.scheduled_at)} · начало: {formatMoscow(run.started_at)}
                {run.completed_at ? ` · завершён: ${formatMoscow(run.completed_at)}` : ""}
                {run.started_at ? ` · получателей: ${run.recipient_count}, завершено: ${run.completed_count}, ошибок: ${run.failed_count}` : ""}
              </Typography>
            </Box>)}
          </Stack> : <Typography color="text.secondary">Завершённых отправок пока не было.</Typography>}
        </Paper>
      </Collapse>}
      <Dialog open={sendDialog} onClose={() => !saving && setSendDialog(false)} fullWidth maxWidth="sm">
        <DialogTitle>{editingSchedule ? "Изменить расписание" : sendWhen === "later" ? "Запланировать рассылку" : "Подтвердить отправку"}</DialogTitle>
        <DialogContent>
          <Stack spacing={2} sx={{ pt: 1 }}>
            {error && <Alert severity="error">{error}</Alert>}
            <Typography>«{campaign?.name}» · {campaign && audiences[campaign.audience_status]}. {editingSchedule ? "Можно добавить даты, изменить время или убрать ожидающие запуски. Уже начавшиеся отправки останутся без изменений." : campaign?.run_status === "scheduled" ? "Эта отправка начнётся сейчас. Уже запланированные даты останутся в расписании, и в них рассылка отправится ещё раз." : `${campaign?.run_count ? "Это повторный запуск: все пользователи, подходящие под фильтр, получат цепочку снова. " : ""}Получатели будут выбраны в момент запуска, и цепочка выполнится один раз.`}</Typography>
            {!editingSchedule && campaign?.run_status !== "scheduled" && <TextField select label="Когда отправить" value={sendWhen} onChange={(e) => setSendWhen(e.target.value as "now" | "later")}>
              <MenuItem value="now">Сейчас</MenuItem>
              <MenuItem value="later">По расписанию</MenuItem>
            </TextField>}
            {sendWhen === "later" && <Paper variant="outlined" sx={{ p: 2, borderRadius: 3 }}>
              <Stack spacing={2}>
                <Stack direction="row" alignItems="center" justifyContent="space-between">
                  <IconButton aria-label="Предыдущий месяц" disabled={calendarMonth <= moscowToday().slice(0, 7)} onClick={() => moveCalendar(-1)}>‹</IconButton>
                  <Typography fontWeight={700} sx={{ textTransform: "capitalize" }}>{prettyMonth(calendarYear, calendarMonthNumber - 1)}</Typography>
                  <IconButton aria-label="Следующий месяц" onClick={() => moveCalendar(1)}>›</IconButton>
                </Stack>
                <Box sx={{ display: "grid", gridTemplateColumns: "repeat(7, 1fr)", gap: 0.5, textAlign: "center" }}>
                  {["Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"].map((day) => <Typography key={day} variant="caption" color="text.secondary" sx={{ py: 0.5 }}>{day}</Typography>)}
                  {Array.from({ length: firstWeekday }, (_, index) => <Box key={`empty-${index}`} />)}
                  {Array.from({ length: calendarDays }, (_, index) => {
                    const date = `${calendarMonth}-${String(index + 1).padStart(2, "0")}`;
                    const selected = date in scheduledDates;
                    return <Button key={date} aria-label={prettyDate(date)} aria-pressed={selected} disabled={date < moscowToday()} variant={selected ? "contained" : "text"} onClick={() => {
                      setScheduledDates((current) => selected ? current : { ...current, [date]: "" });
                      setFocusedDate(date);
                      setError("");
                    }} sx={{ minWidth: 0, aspectRatio: "1", borderRadius: "50%", p: 0, fontWeight: selected ? 700 : 400 }}>{index + 1}</Button>;
                  })}
                </Box>
                {focusedDate && focusedDate in scheduledDates && <Stack direction={{ xs: "column", sm: "row" }} spacing={1.5} alignItems={{ sm: "center" }}>
                  <Typography fontWeight={600} sx={{ flex: 1 }}>{prettyDate(focusedDate)}</Typography>
                  <TextField label="Время начала · МСК" type="time" size="small" value={scheduledDates[focusedDate]} onChange={(e) => setScheduledDates((current) => ({ ...current, [focusedDate]: e.target.value }))} slotProps={{ inputLabel: { shrink: true }, htmlInput: { step: 60 } }} sx={{ width: 180 }} />
                  <Button color="error" onClick={() => { setScheduledDates((current) => { const next = { ...current }; delete next[focusedDate]; return next; }); setFocusedDate(""); }}>Убрать</Button>
                </Stack>}
              </Stack>
            </Paper>}
            {sendWhen === "later" && pickedDates.length > 0 && <Box>
              <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>Запланировано: {pickedDates.length}. Нажмите на дату, чтобы изменить время.</Typography>
              <Stack direction="row" useFlexGap flexWrap="wrap" gap={1}>
                {pickedDates.map(([date, time]) => <Chip key={date} color={focusedDate === date ? "primary" : "default"} label={`${prettyDate(date)}, ${time ? `${time} МСК` : "выберите время"}`} onClick={() => { setFocusedDate(date); setCalendarMonth(date.slice(0, 7)); }} onDelete={() => { setScheduledDates((current) => { const next = { ...current }; delete next[date]; return next; }); if (focusedDate === date) setFocusedDate(""); }} />)}
              </Stack>
            </Box>}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button disabled={saving} onClick={() => setSendDialog(false)}>Отмена</Button>
          <Button variant="contained" disabled={saving || (!editingSchedule && sendWhen === "later" && !pickedDates.length)} onClick={() => void launchMailing()}>{editingSchedule ? "Сохранить расписание" : sendWhen === "now" ? "Подтвердить отправку" : `Запланировать${pickedDates.length ? ` (${pickedDates.length})` : ""}`}</Button>
        </DialogActions>
      </Dialog>
      {!manual && <Alert severity="info" sx={{ mb: 3 }}>
        После последнего шага цепочка начинается заново. При смене статуса
        пользователь выходит из аудитории этой рассылки; если снова подойдёт под
        фильтр, начнёт с первого шага. Если перед повтором нет задержки, новый
        круг начнётся через час.
      </Alert>}
      {error && !draft && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}
      {loading ? (
        <CircularProgress />
      ) : !steps.length ? (
        <Paper variant="outlined" className="empty">
          <Typography variant="h6">Цепочка пока пуста</Typography>
          <Typography sx={{ mt: 1 }}>
            Добавьте сообщения и задержки между ними. Блоки можно перетаскивать.
          </Typography>
        </Paper>
      ) : (
        <Stack spacing={2}>
          {steps.map((step, index) => {
            const m = messages.find((item) => item.id === step.message_id);
            return (
              <Paper
                variant="outlined"
                key={step.id}
                sx={{
                  p: 3,
                  borderColor:
                    draggedID === step.id ? "primary.main" : undefined,
                }}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  if (draggedID !== null) void moveBlock(draggedID, step.id);
                  setDraggedID(null);
                }}
              >
                <Stack
                  direction="row"
                  justifyContent="space-between"
                  alignItems="center"
                >
                  <Stack direction="row" spacing={2} alignItems="center">
                    {canEdit && <Box
                      draggable
                      onDragStart={(event) => {
                        event.dataTransfer.effectAllowed = "move";
                        setDraggedID(step.id);
                      }}
                      onDragEnd={() => setDraggedID(null)}
                      sx={{ cursor: "grab", userSelect: "none", fontSize: 24 }}
                      aria-label="Перетащить блок"
                    >
                      ⠿
                    </Box>}
                    <Chip label={String(index + 1).padStart(2, "0")} />
                    <div>
                      <Typography fontWeight={600}>
                        {step.kind === "delay"
                          ? `⏱ Задержка ${delayLabel(step.delay_seconds)}`
                          : m?.name || "Сообщение"}
                      </Typography>
                      <Typography variant="caption" color="text.secondary">
                        {step.kind === "delay"
                          ? "Пауза перед следующим сообщением"
                          : "Сообщение рассылки"}
                      </Typography>
                    </div>
                  </Stack>
                  {step.kind === "message" && (
                    <Chip
                      size="small"
                      color={step.enabled ? "success" : "default"}
                      variant="outlined"
                      label={step.enabled ? "Включён" : "Выключен"}
                    />
                  )}
                </Stack>
                <Typography
                  sx={{ my: 2, whiteSpace: "pre-wrap" }}
                  color="text.secondary"
                >
                  {step.kind === "message"
                    ? m?.text.replace(/<[^>]+>/g, "")
                    : "После этой паузы цепочка продолжится."}
                </Typography>
                {canEdit && <Stack direction="row" spacing={1}>
                  <Button
                    variant="outlined"
                    onClick={() =>
                      step.kind === "delay" ? openDelay(step) : edit(step)
                    }
                  >
                    {step.kind === "delay"
                      ? "Изменить задержку"
                      : "Редактировать сообщение"}
                  </Button>
                  <Button
                    disabled={index === 0}
                    onClick={() =>
                      void moveBlock(step.id, steps[index - 1]?.id)
                    }
                  >
                    ↑
                  </Button>
                  <Button
                    disabled={index === steps.length - 1}
                    onClick={() =>
                      void moveBlock(step.id, steps[index + 1]?.id)
                    }
                  >
                    ↓
                  </Button>
                  <Button color="error" onClick={() => void deleteStep(step)}>
                    Удалить
                  </Button>
                </Stack>}
              </Paper>
            );
          })}
        </Stack>
      )}
      <Dialog
        open={!!draft}
        onClose={() => !saving && close()}
        fullWidth
        maxWidth="xl"
      >
        <DialogTitle
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          {draft?.step.id
            ? "Редактировать шаг рассылки"
            : "Новое сообщение рассылки"}
          <IconButton
            aria-label="Закрыть"
            size="small"
            disabled={saving}
            onClick={close}
            sx={{ fontSize: 26, lineHeight: 1 }}
          >
            ×
          </IconButton>
        </DialogTitle>
        <DialogContent>
          <Box
            sx={{
              display: "grid",
              gridTemplateColumns: { xs: "1fr", lg: "minmax(0, 1fr) 360px" },
              gap: 3,
              pt: 1,
            }}
          >
            {draft && message && (
              <Stack spacing={2.5} sx={{ minWidth: 0 }}>
                {manual && campaign?.run_status === "sending" && <Alert severity="error">Рассылка уже отправляется. Изменения сейчас сохранить нельзя.</Alert>}
                {error && <Alert severity="error">{error}</Alert>}
                <TextField
                  label="Название"
                  value={message.name}
                  onChange={(e) => updateMessage({ name: e.target.value })}
                />
                <RichTextEditor
                  key={message.id}
                  value={message.text}
                  parseMode={message.parse_mode}
                  onChange={(text) =>
                    updateMessage({ text, parse_mode: "HTML" })
                  }
                  maxLength={
                    message.media.some((m) => m.type !== "video_note")
                      ? 1024
                      : 3500
                  }
                />
                <TemplateVariablesHelp />
                <Divider />
                <Typography fontWeight={600}>Медиа</Typography>
                <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
                  {(
                    [
                      ["video_note", "кружок", "video/mp4,.mp4", false],
                      [
                        "photo",
                        "фото",
                        "image/jpeg,image/png,.jpg,.jpeg,.png",
                        true,
                      ],
                      ["video", "видео", "video/mp4,.mp4", true],
                    ] as const
                  ).map(([type, label, accept, multiple]) => (
                    <Button
                      key={type}
                      component="label"
                      variant="outlined"
                      disabled={
                        saving ||
                        (type === "video_note"
                          ? message.media.some((m) => m.type === type)
                          : regular.length >= 10)
                      }
                    >
                      + Загрузить {label}
                      <input
                        hidden
                        type="file"
                        accept={accept}
                        multiple={multiple}
                        onChange={(e) => {
                          addMedia(Array.from(e.target.files || []), type);
                          e.target.value = "";
                        }}
                      />
                    </Button>
                  ))}
                </Stack>
                <Typography variant="caption" color="text.secondary">
                  До 10 фото или видео и один кружок. Фото — JPEG/PNG до 10 МБ,
                  видео — MP4 до 50 МБ.
                </Typography>
                {[...message.media]
                  .sort((a, b) =>
                    a.type === "video_note"
                      ? -1
                      : b.type === "video_note"
                        ? 1
                        : a.sort_order - b.sort_order,
                  )
                  .map((m) => (
                    <Paper variant="outlined" sx={{ p: 2 }} key={m.id}>
                      <Stack spacing={1.5}>
                        <Typography fontWeight={600}>
                          {m.type === "video_note"
                            ? "Кружок"
                            : m.type === "photo"
                              ? "Фотография"
                              : "Видео"}
                          : {m.name}
                        </Typography>
                        <Typography variant="caption" color="text.secondary">
                          {(m.size / 1024 / 1024).toFixed(1)} МБ
                        </Typography>
                        {m.type === "video_note" && (
                          <TextField
                            select
                            size="small"
                            label="Расположение кружка"
                            value={m.position}
                            onChange={(e) =>
                              updateMessage({
                                media: message.media.map((item) =>
                                  item.id === m.id
                                    ? {
                                        ...item,
                                        position: e.target.value as
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
                        <Stack direction="row" spacing={1}>
                          {m.type !== "video_note" && (
                            <>
                              <Button
                                variant="outlined"
                                disabled={regular[0]?.id === m.id}
                                onClick={() => moveMedia(m.id, -1)}
                              >
                                ←
                              </Button>
                              <Button
                                variant="outlined"
                                disabled={regular.at(-1)?.id === m.id}
                                onClick={() => moveMedia(m.id, 1)}
                              >
                                →
                              </Button>
                            </>
                          )}
                          <Button
                            color="error"
                            variant="outlined"
                            onClick={() => removeMedia(m)}
                          >
                            Удалить
                          </Button>
                        </Stack>
                      </Stack>
                    </Paper>
                  ))}
                <Divider />
                <Typography fontWeight={600}>Кнопки сообщения</Typography>
                {message.buttons.map((b, i) => (
                  <Paper variant="outlined" sx={{ p: 2 }} key={i}>
                    <Stack spacing={1.5}>
                      <Typography variant="caption">Кнопка {i + 1}</Typography>
                      <TextField
                        label="Подпись"
                        value={b.label}
                        onChange={(e) =>
                          changeButton(i, { label: e.target.value })
                        }
                      />
                      <Stack direction="row" spacing={1}>
                        <TextField
                          select
                          label="Тип"
                          fullWidth
                          value={b.type}
                          onChange={(e) =>
                            changeButton(i, {
                              type: e.target.value as MsgButton["type"],
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
                          inputProps={{ min: 0, max: 20 }}
                          onChange={(e) =>
                            changeButton(i, { row: Number(e.target.value) })
                          }
                        />
                      </Stack>
                      {b.type === "callback" ? (
                        <TextField
                          select
                          label="Действие"
                          value={b.action}
                          onChange={(e) =>
                            changeButton(i, { action: e.target.value })
                          }
                        >
                          {actions.map((a) => (
                            <MenuItem value={a} key={a}>
                              {a}
                            </MenuItem>
                          ))}
                        </TextField>
                      ) : (
                        <TextField
                          label="HTTPS URL или переменная ссылки"
                          value={b.action}
                          onChange={(e) =>
                            changeButton(i, { action: e.target.value })
                          }
                        />
                      )}
                      <Stack direction="row" spacing={1}>
                        {([-1, 1] as const).map((direction) => (
                          <Button
                            key={direction}
                            disabled={
                              i + direction < 0 ||
                              i + direction >= message.buttons.length
                            }
                            onClick={() => {
                              const buttons = [...message.buttons];
                              [buttons[i], buttons[i + direction]] = [
                                buttons[i + direction],
                                buttons[i],
                              ];
                              updateMessage({ buttons });
                            }}
                          >
                            {direction < 0 ? "↑" : "↓"}
                          </Button>
                        ))}
                        <Button
                          color="error"
                          onClick={() =>
                            updateMessage({
                              buttons: message.buttons.filter(
                                (_, index) => index !== i,
                              ),
                            })
                          }
                        >
                          Удалить кнопку
                        </Button>
                      </Stack>
                    </Stack>
                  </Paper>
                ))}
                <Button
                  variant="outlined"
                  onClick={() =>
                    updateMessage({
                      buttons: [
                        ...message.buttons,
                        {
                          label: "Подробнее",
                          type: "callback",
                          action: "club:about",
                          row: message.buttons.length,
                          enabled: true,
                        },
                      ],
                    })
                  }
                >
                  + Добавить кнопку
                </Button>
                <Divider />
                <FormControlLabel
                  control={
                    <Switch
                      checked={draft.step.enabled}
                      onChange={(e) =>
                        updateStep({ enabled: e.target.checked })
                      }
                    />
                  }
                  label="Шаг включён"
                />
              </Stack>
            )}
            {message && <MessagePreview message={message} />}
          </Box>
        </DialogContent>
        <DialogActions sx={{ flexWrap: "wrap" }}>
          <Button onClick={close} disabled={saving} sx={{ minWidth: 96 }}>
            {messageSaved ? "Закрыть" : "Отмена"}
          </Button>
          <Button
            variant="contained"
            onClick={() => void save()}
            disabled={saving || messageSaved || (manual && campaign?.run_status === "sending")}
            sx={{ minWidth: 205 }}
          >
            {saving ? "Сохраняем…" : "Сохранить изменения"}
          </Button>
          <Typography
            variant="body2"
            color="success.main"
            fontWeight={600}
            role="status"
            aria-live="polite"
            sx={{
              width: 190,
              flexShrink: 0,
              ml: 1,
              visibility: messageSaved ? "visible" : "hidden",
            }}
          >
            ✓ Изменения сохранены
          </Typography>
        </DialogActions>
      </Dialog>
      <Dialog
        open={!!delayDraft}
        onClose={() => !saving && setDelayDraft(null)}
        fullWidth
        maxWidth="xs"
      >
        <DialogTitle>
          {delayDraft?.id ? "Изменить задержку" : "Добавить задержку"}
        </DialogTitle>
        <DialogContent>
          <Stack spacing={2} sx={{ pt: 1 }}>
            {manual && campaign?.run_status === "sending" && <Alert severity="error">Рассылка уже отправляется. Изменения сейчас сохранить нельзя.</Alert>}
            {error && <Alert severity="error">{error}</Alert>}
            <Stack direction="row" spacing={1}>
              <TextField
                label="Длительность"
                type="number"
                fullWidth
                value={delayAmount}
                inputProps={{ min: 1, step: 1 }}
                onChange={(event) => setDelayAmount(Number(event.target.value))}
              />
              <TextField
                select
                label="Единица"
                value={delayUnit}
                onChange={(event) =>
                  setDelayUnit(event.target.value as typeof delayUnit)
                }
                sx={{ minWidth: 140 }}
              >
                <MenuItem value="seconds">Секунды</MenuItem>
                <MenuItem value="minutes">Минуты</MenuItem>
                <MenuItem value="hours">Часы</MenuItem>
                <MenuItem value="days">Дни</MenuItem>
              </TextField>
            </Stack>
            <Typography variant="body2" color="text.secondary">
              Пауза действует перед следующим сообщением в цепочке.
            </Typography>
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button disabled={saving} onClick={() => setDelayDraft(null)}>
            Отмена
          </Button>
          <Button
            variant="contained"
            disabled={saving || (manual && campaign?.run_status === "sending")}
            onClick={() => void saveDelay()}
          >
            {saving ? "Сохраняем…" : "Сохранить"}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
