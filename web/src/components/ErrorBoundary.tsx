import { Component, type ErrorInfo, type ReactNode } from "react";

/**
 * The last line of defence. A React error thrown during render unmounts the
 * whole tree, and since the body background is a dark token the result is a
 * black screen with no explanation -- the single worst thing this UI can do,
 * because it gives the operator nothing to report and nothing to try.
 *
 * The boundary keeps the page legible and names the failure.
 */
export class ErrorBoundary extends Component<
  { children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The console is where an operator will look first, and it is the only
    // channel that survives the tree being gone.
    console.error("Atlas UI crashed:", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <main className="state state--error" role="alert">
        <p className="state__title">The Atlas UI stopped</p>
        <p className="state__detail">
          Something in the interface failed and the page could not continue. The browser console
          has the details.
        </p>
        <pre className="state__trace">{error.message}</pre>
        <button type="button" className="button" onClick={() => window.location.reload()}>
          Reload
        </button>
      </main>
    );
  }
}
