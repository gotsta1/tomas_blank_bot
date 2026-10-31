import { useCallback, useEffect, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  Navigate,
  NavLink,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Container,
  CssBaseline,
  Paper,
  Stack,
  TextField,
  ThemeProvider,
  Typography,
  createTheme,
} from "@mui/material";
import { api } from "./api";
import { Messages } from "./messages";
import { Broadcasts } from "./broadcasts";
import { Users } from "./users";
import "./style.css";
const theme = createTheme({
  palette: {
    primary: { main: "#225a4c" },
    background: { default: "#f4f5f1", paper: "#fff" },
    text: { primary: "#20372f", secondary: "#69776e" },
  },
  shape: { borderRadius: 12 },
  typography: {
    fontFamily:
      'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    h4: { fontWeight: 650, letterSpacing: "-.035em" },
    button: { textTransform: "none", fontWeight: 600 },
  },
  components: {
    MuiButton: { defaultProps: { disableElevation: true } },
    MuiPaper: { defaultProps: { elevation: 0 } },
    MuiTextField: { defaultProps: { size: "small" } },
  },
});
function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("/login", {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      onLogin();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Container maxWidth="xs" sx={{ pt: "15vh" }}>
      <Typography variant="overline">КЛУБ / УПРАВЛЕНИЕ</Typography>
      <Paper variant="outlined" sx={{ p: 4, mt: 2 }}>
        <Typography variant="h4" mb={1}>
          Добро пожаловать
        </Typography>
        <Typography color="text.secondary" mb={3}>
          Войдите, чтобы управлять ботом.
        </Typography>
        <Box component="form" onSubmit={submit}>
          <Stack spacing={2}>
            {error && <Alert severity="error">{error}</Alert>}
            <TextField
              label="Пароль администратора"
              type="password"
              autoComplete="current-password"
              required
              fullWidth
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Button type="submit" variant="contained" disabled={busy}>
              {busy ? "Входим…" : "Войти"}
            </Button>
          </Stack>
        </Box>
      </Paper>
    </Container>
  );
}
function Shell({ logout }: { logout: () => void }) {
  const location = useLocation();
  const titles: Record<string, string> = {
    "/messages": "Сообщения",
    "/broadcasts": "Авторассылки",
    "/mailings": "Рассылки",
    "/users": "Пользователи",
  };
  return (
    <div className="layout">
      <aside>
        <div className="brand">
          <div className="brand-mark">К</div>
          <div>
            <strong>Клуб</strong>
            <small>Панель управления</small>
          </div>
        </div>
        <div className="nav-caption">РАБОЧЕЕ ПРОСТРАНСТВО</div>
        <nav>
          {[
            ["/messages", "Сообщения"],
            ["/broadcasts", "Авторассылки"],
            ["/mailings", "Рассылки"],
            ["/users", "Пользователи"],
          ].map(([url, name]) => (
            <NavLink key={url} to={url}>
              <span>{name}</span>
            </NavLink>
          ))}
        </nav>
        <div className="aside-bottom">
          <span className="status-dot" /> Администрирование бота
          <Button
            onClick={logout}
            color="inherit"
            size="small"
            sx={{ mt: 2, justifyContent: "flex-start" }}
          >
            Выйти из аккаунта ↗
          </Button>
        </div>
      </aside>
      <main>
        <header>
          <Typography variant="body2" color="text.secondary">
            Рабочее пространство <span className="slash">/</span>{" "}
            {titles[location.pathname] ||
              (location.pathname.startsWith("/broadcasts/")
                ? "Авторассылки"
                : location.pathname.startsWith("/mailings/")
                ? "Рассылки"
                : "Клуб")}
          </Typography>
          <Stack direction="row" alignItems="center" spacing={1}>
            <Chip size="small" label="Администратор" variant="outlined" />
            <Button className="mobile-logout" size="small" onClick={logout}>
              Выйти
            </Button>
          </Stack>
        </header>
        <div className="page">
          <Routes>
            <Route path="/messages" element={<Messages />} />
            <Route path="/broadcasts" element={<Broadcasts />} />
            <Route path="/broadcasts/:id" element={<Broadcasts />} />
            <Route path="/mailings" element={<Broadcasts mode="manual" />} />
            <Route path="/mailings/:id" element={<Broadcasts mode="manual" />} />
            <Route path="/users" element={<Users />} />
            <Route path="*" element={<Navigate to="/messages" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  );
}
function App() {
  const [auth, setAuth] = useState<boolean | null>(null);
  const [error, setError] = useState("");
  const check = useCallback(() => {
    setError("");
    api("/session")
      .then(() => setAuth(true))
      .catch((e) => {
        if (e.status === 401) setAuth(false);
        else setError("Сервер недоступен. Проверьте подключение.");
      });
  }, []);
  useEffect(() => {
    check();
    const expired = () => setAuth(false);
    window.addEventListener("session-expired", expired);
    return () => window.removeEventListener("session-expired", expired);
  }, [check]);
  async function logout() {
    try {
      await api("/logout", { method: "POST" });
      setAuth(false);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  if (error)
    return (
      <Container sx={{ pt: 8 }}>
        <Alert
          severity="error"
          action={<Button onClick={check}>Повторить</Button>}
        >
          {error}
        </Alert>
      </Container>
    );
  if (auth === null)
    return (
      <Box sx={{ p: 8, textAlign: "center" }}>
        <CircularProgress />
      </Box>
    );
  return auth ? (
    <Shell logout={logout} />
  ) : (
    <Login onLogin={() => setAuth(true)} />
  );
}
createRoot(document.getElementById("root")!).render(
  <ThemeProvider theme={theme}>
    <CssBaseline />
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </ThemeProvider>,
);
