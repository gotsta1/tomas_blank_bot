import { useEffect, useRef } from "react";
import { Button, Divider, Paper, Stack, Typography } from "@mui/material";

type ParseMode = "" | "HTML";

function escapeHTML(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function formatNodes(nodes: NodeListOf<ChildNode> | NodeList): string {
  let result = "";
  for (const node of Array.from(nodes)) {
    if (
      node instanceof HTMLElement &&
      (node.tagName === "DIV" || node.tagName === "P") &&
      result &&
      !result.endsWith("\n")
    ) {
      result += "\n";
    }
    result += formatNode(node);
  }
  return result;
}

function formatNode(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE)
    return escapeHTML(node.textContent || "");
  if (!(node instanceof HTMLElement)) return "";

  const tag = node.tagName.toLowerCase();
  // A lone <br> is the browser's placeholder for an empty contentEditable line.
  const children =
    (tag === "div" || tag === "p") &&
    node.childNodes.length === 1 &&
    node.firstChild?.nodeName === "BR"
      ? ""
      : formatNodes(node.childNodes);
  switch (tag) {
    case "b":
    case "strong":
      return `<b>${children}</b>`;
    case "i":
    case "em":
      return `<i>${children}</i>`;
    case "u":
    case "ins":
      return `<u>${children}</u>`;
    case "s":
    case "strike":
    case "del":
      return `<s>${children}</s>`;
    case "code":
      return `<code>${children}</code>`;
    case "pre":
      return `<pre>${children}</pre>`;
    case "blockquote":
      return `<blockquote>${children}</blockquote>`;
    case "br":
      return "\n";
    case "div":
    case "p":
      return `${children}\n`;
    case "span": {
      let result = children;
      const decoration = node.style.textDecoration;
      if (
        node.style.fontWeight === "bold" ||
        Number(node.style.fontWeight) >= 600
      )
        result = `<b>${result}</b>`;
      if (node.style.fontStyle === "italic") result = `<i>${result}</i>`;
      if (decoration.includes("underline")) result = `<u>${result}</u>`;
      if (decoration.includes("line-through")) result = `<s>${result}</s>`;
      return result;
    }
    default:
      return children;
  }
}

function editorHTML(value: string, parseMode: ParseMode) {
  if (parseMode !== "HTML") return escapeHTML(value);
  const parsed = new DOMParser().parseFromString(value, "text/html");
  return formatNodes(parsed.body.childNodes);
}

function telegramHTML(editor: HTMLElement) {
  return formatNodes(editor.childNodes).replace(/\n$/, "");
}

type Props = {
  value: string;
  parseMode: ParseMode;
  onChange: (value: string) => void;
  maxLength?: number;
};

export function RichTextEditor({
  value,
  parseMode,
  onChange,
  maxLength = 3500,
}: Props) {
  const editor = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!editor.current || document.activeElement === editor.current) return;
    editor.current.innerHTML = editorHTML(value, parseMode);
  }, [value, parseMode]);

  function emit() {
    if (editor.current) onChange(telegramHTML(editor.current));
  }

  function command(name: "bold" | "italic" | "underline" | "strikeThrough") {
    editor.current?.focus();
    document.execCommand(name);
    emit();
  }

  return (
    <Paper variant="outlined" className="rich-editor-shell">
      <div className="rich-editor-label">Текст сообщения</div>
      <Stack
        direction="row"
        spacing={0.5}
        alignItems="center"
        className="rich-editor-toolbar"
      >
        <Button
          size="small"
          aria-label="Полужирный"
          title="Полужирный — Cmd/Ctrl+B"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => command("bold")}
          sx={{ minWidth: 34, fontWeight: 800 }}
        >
          B
        </Button>
        <Button
          size="small"
          aria-label="Курсив"
          title="Курсив — Cmd/Ctrl+I"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => command("italic")}
          sx={{ minWidth: 34, fontStyle: "italic" }}
        >
          I
        </Button>
        <Button
          size="small"
          aria-label="Подчёркнутый"
          title="Подчёркнутый — Cmd/Ctrl+U"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => command("underline")}
          sx={{ minWidth: 34, textDecoration: "underline" }}
        >
          U
        </Button>
        <Button
          size="small"
          aria-label="Зачёркнутый"
          title="Зачёркнутый"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => command("strikeThrough")}
          sx={{ minWidth: 34, textDecoration: "line-through" }}
        >
          S
        </Button>
        <Divider orientation="vertical" flexItem />
        <Button
          size="small"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => {
            editor.current?.focus();
            document.execCommand("removeFormat");
            emit();
          }}
        >
          Очистить формат
        </Button>
      </Stack>
      <Divider />
      <div
        ref={editor}
        className="rich-editor"
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        onInput={emit}
        onKeyDown={(event) => {
          if (!(event.metaKey || event.ctrlKey)) return;
          const key = event.key.toLowerCase();
          if (key !== "b" && key !== "i" && key !== "u") return;
          event.preventDefault();
          command(key === "b" ? "bold" : key === "i" ? "italic" : "underline");
        }}
        onPaste={(event) => {
          event.preventDefault();
          document.execCommand(
            "insertText",
            false,
            event.clipboardData.getData("text/plain"),
          );
          emit();
        }}
      />
      <Typography
        variant="caption"
        color={value.length > maxLength ? "error" : "text.secondary"}
        className="rich-editor-counter"
      >
        {value.length} / {maxLength} символов · Cmd/Ctrl+B — жирный, Cmd/Ctrl+I —
        курсив
      </Typography>
    </Paper>
  );
}
