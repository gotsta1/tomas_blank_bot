import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  MenuItem,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import { api } from "./api";
type User = {
  id: number;
  telegram_user_id: string;
  username: string;
  first_name: string;
  status: "new" | "active" | "expired";
  expires_at: string | null;
  days_left: number | null;
  auto_renew: boolean;
  first_started_at: string | null;
  last_activity_at: string;
};
const labels = { new: "Новый", active: "Активна", expired: "Истекла" };
const userCountLabel = (count: number) => {
  const lastTwo = count % 100;
  if (lastTwo >= 11 && lastTwo <= 14) return `${count} пользователей`;
  const last = count % 10;
  if (last === 1) return `${count} пользователь`;
  if (last >= 2 && last <= 4) return `${count} пользователя`;
  return `${count} пользователей`;
};
const date = (value: string | null) =>
  value
    ? new Date(value).toLocaleString("ru-RU", {
        dateStyle: "short",
        timeStyle: "short",
      })
    : "—";
export function Users() {
  const [items, setItems] = useState<User[]>([]);
  const [total, setTotal] = useState(0);
  const [status, setStatus] = useState("");
  const [renew, setRenew] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let live = true;
    setLoading(true);
    const timer = setTimeout(() => {
      api<{ items: User[]; total: number }>(
        "/users?" +
          new URLSearchParams({
            status,
            auto_renew: renew,
            search,
            page: String(page),
          }),
      )
        .then((data) => {
          if (live) {
            setItems(data.items);
            setTotal(data.total);
            setError("");
          }
        })
        .catch((e) => {
          if (live) setError(e.message);
        })
        .finally(() => {
          if (live) setLoading(false);
        });
    }, 250);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [status, renew, search, page, reload]);
  return (
    <>
      <div className="page-heading">
        <div>
          <Typography variant="h4">Пользователи</Typography>
          <Typography color="text.secondary" sx={{ mt: 1 }}>
            Участники, оплаченный доступ и автопродление.
          </Typography>
        </div>
        <Chip variant="outlined" label={userCountLabel(total)} />
      </div>
      <Paper variant="outlined" sx={{ p: 2.5, mb: 2 }}>
        <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
          <TextField
            label="Telegram ID или username"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(1);
            }}
            sx={{ flex: 1 }}
          />
          <TextField
            select
            label="Статус"
            value={status}
            onChange={(e) => {
              setStatus(e.target.value);
              setPage(1);
            }}
            sx={{ minWidth: 155 }}
          >
            <MenuItem value="">Все статусы</MenuItem>
            {Object.entries(labels).map(([k, v]) => (
              <MenuItem key={k} value={k}>
                {v}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            select
            label="Автопродление"
            value={renew}
            onChange={(e) => {
              setRenew(e.target.value);
              setPage(1);
            }}
            sx={{ minWidth: 160 }}
          >
            <MenuItem value="">Любое</MenuItem>
            <MenuItem value="true">Включено</MenuItem>
            <MenuItem value="false">Выключено</MenuItem>
          </TextField>
        </Stack>
      </Paper>
      {error && (
        <Alert
          severity="error"
          action={
            <Button onClick={() => setReload((x) => x + 1)}>Повторить</Button>
          }
          sx={{ mb: 2 }}
        >
          {error}
        </Alert>
      )}
      <Paper variant="outlined" sx={{ overflow: "hidden" }}>
        {!loading && items.length === 0 ? (
          <div className="empty">Пользователи не найдены</div>
        ) : (
          <TableContainer>
            <Table size="small" sx={{ minWidth: 1000 }}>
              <TableHead>
                <TableRow>
                  {[
                    "Участник",
                    "Telegram ID",
                    "Статус",
                    "Подписка до",
                    "Дней",
                    "Автопродление",
                    "Первый /start",
                    "Активность",
                  ].map((label) => (
                    <TableCell
                      key={label}
                      sx={{
                        py: 2,
                        color: "text.secondary",
                        fontSize: 11,
                        whiteSpace: "nowrap",
                      }}
                    >
                      {label}
                    </TableCell>
                  ))}
                </TableRow>
              </TableHead>
              <TableBody>
                {loading ? (
                  <TableRow>
                    <TableCell colSpan={8} align="center" sx={{ py: 6 }}>
                      <CircularProgress size={24} />
                    </TableCell>
                  </TableRow>
                ) : items.length ? (
                  items.map((u) => (
                    <TableRow key={u.id} hover>
                      <TableCell sx={{ py: 2 }}>
                        <Typography variant="body2" fontWeight={600}>
                          {u.first_name || "Без имени"}
                        </Typography>
                        <Typography variant="caption" color="text.secondary">
                          {u.username ? "@" + u.username : "—"}
                        </Typography>
                      </TableCell>
                      <TableCell sx={{ fontFamily: "monospace", fontSize: 12 }}>
                        {u.telegram_user_id}
                      </TableCell>
                      <TableCell>
                        <Chip
                          size="small"
                          variant="outlined"
                          color={
                            u.status === "active"
                              ? "success"
                              : u.status === "expired"
                                ? "warning"
                                : "default"
                          }
                          label={labels[u.status]}
                        />
                      </TableCell>
                      <TableCell>{date(u.expires_at)}</TableCell>
                      <TableCell>{u.days_left ?? "—"}</TableCell>
                      <TableCell>
                        {u.auto_renew ? "Включено" : "Выключено"}
                      </TableCell>
                      <TableCell>{date(u.first_started_at)}</TableCell>
                      <TableCell>{date(u.last_activity_at)}</TableCell>
                    </TableRow>
                  ))
                ) : (
                  <TableRow>
                    <TableCell colSpan={8} align="center" sx={{ py: 8 }}>
                      Пользователи не найдены
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </TableContainer>
        )}
        <Stack
          direction="row"
          justifyContent="space-between"
          alignItems="center"
          sx={{ px: 2, py: 1.5 }}
        >
          <Typography variant="caption" color="text.secondary">
            Страница {page} · по 50 пользователей
          </Typography>
          <Stack direction="row">
            <Button
              disabled={page === 1 || loading}
              onClick={() => setPage((p) => p - 1)}
            >
              Назад
            </Button>
            <Button
              disabled={page * 50 >= total || loading}
              onClick={() => setPage((p) => p + 1)}
            >
              Далее
            </Button>
          </Stack>
        </Stack>
      </Paper>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ display: "block", mt: 2 }}
      >
        Даты отображаются в часовом поясе вашего браузера.
      </Typography>
    </>
  );
}
