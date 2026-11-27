import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button, Empty, Header } from "./views/common";

export default class AppErrorBoundary extends Component<
  { children: ReactNode },
  { error: string }
> {
  state = { error: "" };

  static getDerivedStateFromError(error: unknown) {
    return { error: error instanceof Error ? error.message : String(error) };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Inkuna view failed", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <main className="min-w-0 px-[clamp(28px,5vw,72px)] pt-10 pb-20 max-[600px]:px-[18px] max-[600px]:py-6">
          <section>
            <Header eyebrow="Display error" title="Something went wrong" />
            <div role="alert">
              <Empty title="Inkuna could not display this view">
                <p className="wrap-anywhere">{this.state.error}</p>
                <Button
                  variant="primary"
                  onClick={() => window.location.reload()}
                >
                  Reload Inkuna
                </Button>
              </Empty>
            </div>
          </section>
        </main>
      );
    }
    return this.props.children;
  }
}
