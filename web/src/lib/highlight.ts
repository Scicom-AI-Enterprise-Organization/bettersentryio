/**
 * A very small syntax highlighter for the onboarding snippets.
 *
 * Deliberately not a dependency: the only languages we ever render are the ones this
 * app generates itself (Python, shell, YAML, dotenv), and a real highlighter costs
 * megabytes plus a build step for four fixed grammars. It runs server-side, so the
 * browser gets plain spans with no highlighting JS at all.
 *
 * It is a tokenizer, not a parser — it will mis-colour pathological input. That is an
 * acceptable trade for snippets we author, and it can never throw: anything unmatched
 * falls through as plain text.
 */

export type Token = { text: string; kind: TokenKind };

export type TokenKind =
  | "plain"
  | "comment"
  | "string"
  | "keyword"
  | "constant"
  | "number"
  | "function"
  | "decorator"
  | "variable"
  | "key"
  | "flag";

type Rule = { kind: TokenKind; re: RegExp };

const PY_KEYWORDS =
  /^(?:import|from|as|def|class|async|await|while|for|if|elif|else|return|yield|global|nonlocal|with|try|except|finally|raise|pass|break|continue|lambda|del|assert|in|is|not|and|or)\b/;
const PY_CONSTANTS = /^(?:True|False|None|self)\b/;

/** Longest-match-first within each language; order inside the array is the priority. */
const RULES: Record<string, Rule[]> = {
  python: [
    { kind: "comment", re: /^#[^\n]*/ },
    { kind: "string", re: /^(?:"""[\s\S]*?"""|'''[\s\S]*?'''|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*')/ },
    { kind: "decorator", re: /^@[A-Za-z_][\w.]*/ },
    { kind: "keyword", re: PY_KEYWORDS },
    { kind: "constant", re: PY_CONSTANTS },
    { kind: "number", re: /^\d+(?:\.\d+)?/ },
    { kind: "function", re: /^[A-Za-z_]\w*(?=\s*\()/ },
  ],
  bash: [
    { kind: "comment", re: /^#[^\n]*/ },
    { kind: "string", re: /^(?:"(?:\\.|[^"\\])*"|'(?:[^'])*')/ },
    { kind: "variable", re: /^\$(?:\{[^}]*\}|[A-Za-z_]\w*)/ },
    { kind: "keyword", re: /^(?:export|curl|while|do|done|if|then|fi|sleep|echo|wc|source)\b/ },
    { kind: "flag", re: /^-{1,2}[A-Za-z][\w-]*/ },
    { kind: "number", re: /^\d+(?:\.\d+)?/ },
  ],
  yaml: [
    { kind: "comment", re: /^#[^\n]*/ },
    { kind: "string", re: /^(?:"(?:\\.|[^"\\])*"|'(?:[^'])*')/ },
    { kind: "key", re: /^[A-Za-z_][\w.-]*(?=\s*:)/ },
    { kind: "variable", re: /^\$(?:\{[^}]*\}|[A-Za-z_]\w*)/ },
    { kind: "constant", re: /^(?:true|false|null)\b/ },
    { kind: "number", re: /^\d+(?:\.\d+)?/ },
  ],
  dotenv: [
    { kind: "comment", re: /^#[^\n]*/ },
    { kind: "key", re: /^[A-Z_][A-Z0-9_]*(?==)/ },
  ],
  cron: [
    { kind: "comment", re: /^#[^\n]*/ },
    { kind: "string", re: /^(?:"(?:\\.|[^"\\])*"|'(?:[^'])*')/ },
    { kind: "variable", re: /^\$(?:\{[^}]*\}|[A-Za-z_]\w*)/ },
    { kind: "number", re: /^[\d*/,-]+(?=\s)/ },
    { kind: "flag", re: /^-{1,2}[A-Za-z][\w-]*/ },
  ],
};

/** Languages that have no rules render as plain text rather than failing. */
export function highlight(code: string, language = "plain"): Token[] {
  const rules = RULES[language];
  if (!rules) return [{ text: code, kind: "plain" }];

  const out: Token[] = [];
  let rest = code;
  let buffer = "";

  const flush = () => {
    if (buffer) {
      out.push({ text: buffer, kind: "plain" });
      buffer = "";
    }
  };

  while (rest.length > 0) {
    let matched = false;
    for (const rule of rules) {
      const m = rule.re.exec(rest);
      if (m && m[0].length > 0) {
        flush();
        out.push({ text: m[0], kind: rule.kind });
        rest = rest.slice(m[0].length);
        matched = true;
        break;
      }
    }
    if (!matched) {
      // Consume a whole identifier at once so `import` inside `important` cannot be
      // matched as a keyword on the next pass.
      const word = /^[A-Za-z_]\w*/.exec(rest);
      const chunk = word ? word[0] : rest[0];
      buffer += chunk;
      rest = rest.slice(chunk.length);
    }
  }
  flush();
  return out;
}

/**
 * Token colours. The code surface is dark in both themes — the same choice Sentry
 * makes — so one palette is correct everywhere and there is no second set to keep in
 * sync. Values are literal because this surface deliberately does not follow the page.
 */
export const TOKEN_CLASS: Record<TokenKind, string> = {
  plain: "text-zinc-200",
  comment: "text-zinc-500 italic",
  string: "text-emerald-300",
  keyword: "text-rose-300",
  constant: "text-sky-300",
  number: "text-amber-200",
  function: "text-violet-300",
  decorator: "text-amber-300",
  variable: "text-sky-300",
  key: "text-sky-300",
  flag: "text-zinc-400",
};
