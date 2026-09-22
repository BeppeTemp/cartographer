import { useEffect, useRef, useState } from "react";

/**
 * The bearer-token prompt.
 *
 * The field is a password input so the token is not shoulder-read or captured
 * by a screenshot, it is never placed in the URL, and "remember" is explicitly
 * scoped to this tab: sessionStorage dies when the tab does, while
 * localStorage would leave a working credential on the machine indefinitely.
 */
export function AuthPrompt({
  onSubmit,
  rejected,
}: {
  onSubmit(token: string, remember: boolean): void;
  rejected: boolean;
}) {
  const [token, setToken] = useState("");
  const [remember, setRemember] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => inputRef.current?.focus(), []);

  return (
    <main className="auth">
      <form
        className="auth__card panel"
        onSubmit={(event) => {
          event.preventDefault();
          if (token.trim()) onSubmit(token.trim(), remember);
        }}
      >
        <h1 className="auth__title">Cartographer Atlas</h1>
        <p className="auth__detail">
          This server requires a bearer token. It is kept in memory for this session and never
          written to the address bar or to persistent storage.
        </p>
        {rejected && (
          <p className="auth__error" role="alert">
            That token was not accepted.
          </p>
        )}
        <label className="auth__field">
          <span>Bearer token</span>
          <input
            ref={inputRef}
            className="input"
            type="password"
            value={token}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setToken(event.target.value)}
          />
        </label>
        <label className="auth__remember">
          <input
            type="checkbox"
            checked={remember}
            onChange={(event) => setRemember(event.target.checked)}
          />
          <span>Remember for this tab</span>
        </label>
        <button type="submit" className="button button--primary" disabled={!token.trim()}>
          Open the atlas
        </button>
      </form>
    </main>
  );
}
