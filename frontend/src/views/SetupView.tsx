import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  deleteSecret,
  getCapabilities,
  getConfiguration,
  removeSecretDeclaration,
  saveConfiguration,
  saveSecret,
  validateConfiguration,
} from "../bridge";
import { useInkunaEvent } from "../events";
import {
  emptyConfigFile,
  type CapabilitiesInfo,
  type ConfigFieldError,
  type ConfigFile,
  type ConfigurationInfo,
} from "../types";
import { Alert, Button, Chip, Header, Notice } from "./common";
import { ConcurrencyPanel } from "./setup/ConcurrencyPanel";
import { DiskUsagePanel } from "./setup/DiskUsagePanel";
import { IntegrationsPanel } from "./setup/IntegrationsPanel";
import { MCPServersPanel } from "./setup/MCPServersPanel";
import { NoticesPanel } from "./setup/NoticesPanel";
import { OrchestratorPanel } from "./setup/OrchestratorPanel";
import { FieldErrorsContext, OverridesContext } from "./setup/overrides";
import { PricingPanel } from "./setup/PricingPanel";
import { RepositoriesPanel } from "./setup/RepositoriesPanel";
import { RoutingPanel } from "./setup/RoutingPanel";
import { SecretsPanel } from "./setup/SecretsPanel";
import { StageLibraryPanel } from "./setup/StageLibraryPanel";
import { StoragePanel } from "./setup/StoragePanel";
import { TimeoutsPanel } from "./setup/TimeoutsPanel";

// Go encodes nil slices as null; normalize so list rendering never breaks.
function mergeFile(file: Partial<ConfigFile> | undefined): ConfigFile {
  const merged = { ...emptyConfigFile, ...(file ?? {}) };
  return {
    ...merged,
    repositories: merged.repositories ?? [],
    secrets: merged.secrets ?? [],
    agents: merged.agents ?? {},
    stages: merged.stages ?? {},
    routing: merged.routing ?? {},
  };
}

export function SetupView({
  onSaved,
  onDirtyChange,
}: {
  onSaved(): void;
  /** Says whether the form has edits not yet saved. */
  onDirtyChange?(dirty: boolean): void;
}) {
  const [info, setInfo] = useState<ConfigurationInfo | null>(null);
  const [registered, setRegistered] = useState<CapabilitiesInfo | null>(null);
  const [file, setFile] = useState<ConfigFile>(emptyConfigFile);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [saving, setSaving] = useState(false);
  const [problems, setProblems] = useState<ConfigFieldError[]>([]);
  // Unsaved edits survive a configuration event; a clean form follows it.
  // The ref is read where a state would be a render behind; markDirty keeps
  // the app told, so leaving the screen asks first.
  const dirty = useRef(false);
  const markDirty = useCallback(
    (value: boolean) => {
      dirty.current = value;
      onDirtyChange?.(value);
    },
    [onDirtyChange],
  );
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

  const load = useCallback(() => {
    getConfiguration()
      .then((value) => {
        setInfo(value);
        if (!dirty.current) setFile(mergeFile(value.file));
      })
      .catch((reason: unknown) => setError(String(reason)));
  }, []);
  useEffect(load, [load]);
  // What a build registered is fixed when it starts, so it is read once.
  useEffect(() => {
    getCapabilities()
      .then(setRegistered)
      .catch((reason: unknown) => setError(String(reason)));
  }, []);
  // Go emits this after every save, including ones made outside this view.
  useInkunaEvent("inkuna:config", load);

  const extensionLabels = (registered?.capabilities ?? []).map(
    (capability) => capability.label,
  );
  const overrides = useMemo(
    () =>
      new Map(
        (info?.overrides ?? []).map((override) => [override.field, override]),
      ),
    [info],
  );

  function apply(value: ConfigurationInfo, message: string) {
    markDirty(false);
    setInfo(value);
    setFile(mergeFile(value.file));
    setProblems([]);
    setStatus(message);
    setError("");
    onSaved();
  }

  // Keychain changes leave inkuna.yaml alone, so unsaved edits stay.
  function applySecrets(value: ConfigurationInfo, message: string) {
    setInfo(value);
    if (!dirty.current) setFile(mergeFile(value.file));
    setStatus(message);
    setError("");
    onSaved();
  }

  async function save() {
    setSaving(true);
    setStatus("");
    try {
      // Runtimes are edited in their own view. This draft may predate a change
      // made there, so the save carries the saved runtimes, not the draft's.
      const latest = await getConfiguration();
      const encoded = JSON.stringify({
        ...file,
        runtimes: latest.file.runtimes ?? file.runtimes,
        version: 1,
      });
      const found = await validateConfiguration(encoded);
      setProblems(found);
      if (found.length > 0) {
        setError(
          found.length === 1
            ? "One setting needs attention; see the message beside it."
            : `${found.length} settings need attention; see the messages beside them.`,
        );
        return;
      }
      const saved = await saveConfiguration(encoded);
      apply(
        saved,
        saved.restartRequired
          ? "Configuration saved. Restart Inkuna to use the new data directory; nothing was moved."
          : "Configuration saved",
      );
    } catch (reason) {
      setError(String(reason));
    } finally {
      setSaving(false);
    }
  }

  // The value is passed straight through to the keychain and never kept in
  // state here; the panel clears its own input before calling.
  async function storeSecret(name: string, value: string): Promise<boolean> {
    setSaving(true);
    try {
      applySecrets(
        await saveSecret(name, value),
        `Stored ${name} in the keychain`,
      );
      return true;
    } catch (reason) {
      setError(String(reason));
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function removeSecret(name: string) {
    setSaving(true);
    try {
      applySecrets(
        await deleteSecret(name),
        `Removed ${name} from the keychain`,
      );
    } catch (reason) {
      setError(String(reason));
    } finally {
      setSaving(false);
    }
  }

  async function forgetSecret(name: string) {
    setSaving(true);
    try {
      const value = await removeSecretDeclaration(name);
      // Go already saved the removal; repeat it in a draft with unsaved edits
      // so the next save does not declare the secret again.
      if (dirty.current) {
        setFile((current) => {
          const draft: ConfigFile = JSON.parse(JSON.stringify(current));
          draft.secrets = draft.secrets.filter(
            (secret) => secret.name !== name,
          );
          for (const runtime of Object.values(draft.runtimes)) {
            runtime.secrets = runtime.secrets?.filter((item) => item !== name);
          }
          return draft;
        });
      }
      applySecrets(value, `Forgot ${name}: value, declaration, and exports`);
    } catch (reason) {
      setError(String(reason));
    } finally {
      setSaving(false);
    }
  }

  function update(mutate: (draft: ConfigFile) => void) {
    markDirty(true);
    setFile((current) => {
      const draft: ConfigFile = JSON.parse(JSON.stringify(current));
      mutate(draft);
      return draft;
    });
  }

  return (
    <section>
      <Header
        eyebrow="Configuration"
        title="Settings"
        actions={
          <Button
            variant="primary"
            disabled={saving}
            onClick={() => void save()}
          >
            {saving ? "Saving…" : "Save configuration"}
          </Button>
        }
      />
      {error && <Alert>{error}</Alert>}
      {status && (
        <div className="mb-4">
          <Notice role="status">
            <b className="text-ink">{status}</b>
          </Notice>
        </div>
      )}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Chip tone={info?.exists ? "green" : "neutral"}>
          {info?.exists ? "inkuna.yaml found" : "inkuna.yaml not created yet"}
        </Chip>
        <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
          {info?.path || "path resolved at save"}
        </span>
        {info?.dataDir && (
          <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
            data dir {info.dataDir}
          </span>
        )}
        {registered && !registered.product.inkuna && (
          <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
            {registered.product.name}
            {registered.product.version && ` ${registered.product.version}`}
          </span>
        )}
        {extensionLabels.length > 0 && (
          <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
            extensions add {extensionLabels.join(", ")}
          </span>
        )}
        {info?.build && (
          <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
            Inkuna {info.build.version} · commit {info.build.commit}
          </span>
        )}
      </div>
      <OverridesContext.Provider value={overrides}>
        <FieldErrorsContext.Provider value={problems}>
          <div className="grid grid-cols-2 gap-4 max-[900px]:grid-cols-1">
            <OrchestratorPanel
              orchestrator={file.orchestrator}
              update={update}
            />
            <ConcurrencyPanel
              concurrency={file.concurrency}
              notifications={file.notifications}
              update={update}
            />
            <RepositoriesPanel
              repositories={file.repositories}
              trackers={info?.trackers ?? []}
              update={update}
              onError={setError}
            />
            <SecretsPanel
              secrets={info?.secrets ?? []}
              declared={file.secrets}
              saving={saving}
              update={update}
              onStore={storeSecret}
              onRemove={(name) => void removeSecret(name)}
              onForget={(name) => void forgetSecret(name)}
            />
            <MCPServersPanel
              servers={file.mcpServers ?? []}
              secrets={file.secrets}
              update={update}
            />
            <RoutingPanel
              routing={file.routing}
              builtIn={info?.routingDefaults}
              update={update}
            />
            <PricingPanel
              pricing={file.pricing}
              builtIn={info?.pricingDefaults}
              update={update}
            />
            <TimeoutsPanel
              values={file.timeouts}
              defaults={info?.defaults?.timeouts}
              update={update}
            />
            <IntegrationsPanel
              linear={file.linear}
              github={file.github}
              defaults={info?.defaults}
              update={update}
            />
            <StoragePanel
              directories={file.agents.directories ?? []}
              dataDir={file.dataDir}
              effectiveDataDir={info?.dataDir ?? ""}
              update={update}
              onError={setError}
            />
            <StageLibraryPanel
              directories={file.stages.directories ?? []}
              effective={info?.stageDirectories ?? []}
              problems={info?.libraryProblems ?? []}
              update={update}
              onError={setError}
            />
            <DiskUsagePanel />
            <NoticesPanel />
          </div>
        </FieldErrorsContext.Provider>
      </OverridesContext.Provider>
    </section>
  );
}
