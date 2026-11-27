import { useState } from "react";
import { Alert, Button, Code } from "../common";
import { loadNotices } from "./notices";
import { SetupPanel } from "./shared";

/** The copyright and license text of the third-party code Inkuna ships. */
export function NoticesPanel({
  load = loadNotices,
}: {
  load?: () => Promise<string>;
}) {
  const [notices, setNotices] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function show() {
    setLoading(true);
    try {
      setNotices(await load());
      setError("");
    } catch (reason) {
      setError(`Could not load the notices: ${String(reason)}`);
    } finally {
      setLoading(false);
    }
  }

  return (
    <SetupPanel
      title="Third-party notices"
      wide
      description="Inkuna is built from open-source Go modules and npm packages. Their licenses ask that their copyright and license text go with every copy."
    >
      <div className="mt-4">
        {error && <Alert>{error}</Alert>}
        {notices === null ? (
          <Button disabled={loading} onClick={() => void show()}>
            {loading ? "Loading…" : "Show notices"}
          </Button>
        ) : (
          <Code tall>{notices}</Code>
        )}
      </div>
    </SetupPanel>
  );
}
