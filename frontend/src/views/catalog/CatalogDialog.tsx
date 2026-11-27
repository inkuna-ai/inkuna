import { useEffect, useState } from "react";
import { addCatalogItem, listCatalog, previewCatalogItem } from "../../bridge";
import type {
  CatalogEntry,
  CatalogKind,
  CatalogListing,
  CatalogResult,
  SourceReport,
} from "../../types";
import {
  Alert,
  Button,
  Chip,
  Code,
  Disclosure,
  Modal,
  Notice,
} from "../common";

const kindLabels: Record<CatalogKind, string> = {
  agent: "agent",
  skill: "skill",
  rule: "rule",
  stage: "shared stage",
  group: "group",
};

type Pending = { entry: CatalogEntry; global: boolean };

/** An entry's key: names are unique within one catalog, not across sources. */
function entryKey(entry: CatalogEntry): string {
  return `${entry.item.source ?? ""}:${entry.item.name}`;
}

/**
 * The catalog's items of some kinds, each added to the project or this
 * machine's global directories after Go says what the add brings with it.
 * Go decides every add, so a refusal is shown with Go's reason, and what a
 * place already has is Go's listing of it.
 */
export function CatalogDialog({
  title,
  kinds,
  projectID,
  allowGlobal,
  repositoryBlocked = "",
  onClose,
  onAdded,
}: {
  title: string;
  kinds: CatalogKind[];
  /** The project to add to; "" offers only global adds. */
  projectID: string;
  /** Whether items may be added globally; a rule never can be. */
  allowGlobal: boolean;
  /** Why nothing may be added to the project now, such as unsaved edits. */
  repositoryBlocked?: string;
  onClose(): void;
  onAdded(result: CatalogResult, global: boolean): void;
}) {
  const [listing, setListing] = useState<CatalogListing | null>(null);
  const [loadError, setLoadError] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  // While Go writes an add, the dialog stays open: closing it would let the
  // person edit the view that the add's result then reloads over.
  const [adding, setAdding] = useState(false);

  useEffect(() => {
    let active = true;
    listCatalog(projectID)
      .then((value) => active && setListing(value))
      .catch((reason: unknown) => active && setLoadError(String(reason)));
    return () => {
      active = false;
    };
  }, [projectID]);

  const entries = (listing?.items ?? []).filter((entry) =>
    kinds.includes(entry.item.kind),
  );
  return (
    <Modal
      title={title}
      wide
      onClose={() => {
        if (!adding) onClose();
      }}
    >
      {pending ? (
        <Confirm
          key={`${entryKey(pending.entry)}:${pending.global}`}
          pending={pending}
          projectID={projectID}
          busy={adding}
          onBusyChange={setAdding}
          onBack={() => setPending(null)}
          onAdded={(result) => onAdded(result, pending.global)}
        />
      ) : (
        <>
          <p className="mt-0 mb-4 text-sm text-muted">
            Adding copies an item into{" "}
            {projectID ? "this repository's .inkuna directory or " : ""}
            the global directories, with what it needs that is missing there.
            From then on it is an ordinary file, edited like any other, and a
            later Inkuna never changes it.
          </p>
          {loadError && (
            <Alert>The catalog could not be loaded: {loadError}</Alert>
          )}
          {listing?.repositoryProblem && (
            <Alert>
              Nothing can be added to this repository until this is fixed:{" "}
              {listing.repositoryProblem}
            </Alert>
          )}
          {listing?.globalProblem && allowGlobal && (
            <Alert>
              Nothing can be added globally until this is fixed:{" "}
              {listing.globalProblem}
            </Alert>
          )}
          {repositoryBlocked && projectID && (
            <Notice role="status" className="mt-0 mb-4">
              {repositoryBlocked}
            </Notice>
          )}
          {(listing?.sources ?? []).map((report) => (
            <SourceNotice key={report.source} report={report} />
          ))}
          {!listing && !loadError && (
            <p className="m-0 text-xs text-muted">Loading the catalog…</p>
          )}
          {listing && entries.length === 0 && (
            <p className="m-0 text-xs text-muted">
              The catalog has no{" "}
              {kinds.map((kind) => `${kindLabels[kind]}s`).join(" or ")}.
            </p>
          )}
          <ul
            aria-label="Catalog items"
            className="m-0 grid max-h-[60vh] list-none gap-2 overflow-y-auto p-0"
          >
            {entries.map((entry) => (
              <CatalogRow
                key={entryKey(entry)}
                entry={entry}
                canAddToRepository={
                  Boolean(projectID) &&
                  !repositoryBlocked &&
                  !listing?.repositoryProblem
                }
                canAddGlobally={
                  allowGlobal && !entry.globalRefusal && !listing?.globalProblem
                }
                showRepository={Boolean(projectID)}
                onAdd={(global) => setPending({ entry, global })}
              />
            ))}
          </ul>
          <div className="mt-5 flex justify-end">
            <Button onClick={onClose}>Close</Button>
          </div>
        </>
      )}
    </Modal>
  );
}

function CatalogRow({
  entry,
  canAddToRepository,
  canAddGlobally,
  showRepository,
  onAdd,
}: {
  entry: CatalogEntry;
  canAddToRepository: boolean;
  canAddGlobally: boolean;
  showRepository: boolean;
  onAdd(global: boolean): void;
}) {
  const { item } = entry;
  const uses = [
    ...(item.needs.agents ?? []).map((name) => `agent ${name}`),
    ...(item.needs.stages ?? []).map((name) => name),
    ...(item.needs.skills ?? []).map((name) => `skill ${name}`),
  ];
  return (
    <li
      aria-label={item.name}
      className="min-w-0 border border-line bg-field p-3"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <b className="min-w-0 text-sm wrap-anywhere">{item.name}</b>
        <Chip tone="accent">{kindLabels[item.kind]}</Chip>
        {item.permission && <Chip>{item.permission}</Chip>}
        {item.source && (
          <Chip>
            from {item.source}
            {item.scope ? `, shared with ${item.scope}` : ""}
          </Chip>
        )}
        {entry.repository && <Chip tone="green">in this repository</Chip>}
        {entry.global && <Chip>global</Chip>}
      </div>
      <p className="mt-1.5 mb-0 text-xs leading-relaxed break-words text-muted">
        {item.description}
      </p>
      <dl className="my-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
        <dt className="text-muted">Task kinds</dt>
        <dd className="m-0 min-w-0 break-words">
          {item.taskKinds?.join(", ") || "any"}
        </dd>
        {(item.kind !== "agent" || uses.length > 0) && (
          <>
            <dt className="text-muted">Uses</dt>
            <dd className="m-0 min-w-0 break-words">
              {uses.join(", ") || "nothing shared"}
            </dd>
          </>
        )}
      </dl>
      {entry.repository && (
        <p className="m-0 text-[11px] wrap-anywhere text-muted">
          Already in this repository:{" "}
          <span className="font-mono">{entry.repository}</span>
        </p>
      )}
      {entry.global && (
        <p className="m-0 text-[11px] wrap-anywhere text-muted">
          Already global: <span className="font-mono">{entry.global}</span>
        </p>
      )}
      <Disclosure label={`File ${item.path}`}>
        <Code>{item.content}</Code>
      </Disclosure>
      {(item.files ?? []).length > 0 && (
        <p className="m-0 text-[11px] wrap-anywhere text-muted">
          With {(item.files ?? []).join(", ")}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-1.5">
        {showRepository && !entry.repository && (
          <Button
            aria-label={`Add ${item.name} to the repository`}
            disabled={!canAddToRepository}
            onClick={() => onAdd(false)}
          >
            Add to repository
          </Button>
        )}
        {canAddGlobally && !entry.global && (
          <Button
            aria-label={`Add ${item.name} globally`}
            onClick={() => onAdd(true)}
          >
            Add globally
          </Button>
        )}
      </div>
    </li>
  );
}

/**
 * Go's preview of one add: the item and what comes with it, and the notes,
 * such as the built-in rules being written with a first rule. Adding asks
 * Go again, which checks everything afresh.
 */
function Confirm({
  pending,
  projectID,
  busy,
  onBusyChange: setBusy,
  onBack,
  onAdded,
}: {
  pending: Pending;
  projectID: string;
  /** Whether Go is writing the add. */
  busy: boolean;
  onBusyChange(busy: boolean): void;
  onBack(): void;
  onAdded(result: CatalogResult): void;
}) {
  const { item } = pending.entry;
  const [preview, setPreview] = useState<CatalogResult | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    previewCatalogItem(projectID, item.name, item.source ?? "", pending.global)
      .then((value) => active && setPreview(value))
      .catch((reason: unknown) => active && setError(String(reason)));
    return () => {
      active = false;
    };
  }, [projectID, item.name, item.source, pending.global]);

  async function add() {
    setBusy(true);
    setError("");
    let result: CatalogResult;
    try {
      result = await addCatalogItem(
        projectID,
        item.name,
        item.source ?? "",
        pending.global,
      );
    } catch (reason) {
      setError(String(reason));
      setBusy(false);
      return;
    }
    setBusy(false);
    onAdded(result);
  }

  const added = preview?.added ?? [];
  const brought = added.slice(1);
  return (
    <section aria-label={`Add ${item.name}`}>
      <p className="mt-0 mb-3 text-sm text-ink">
        Add {kindLabels[item.kind]} <b>{item.name}</b>{" "}
        {pending.global ? "to the global directories" : "to this repository"}.
      </p>
      {error && <Alert>Go refused the add: {error}</Alert>}
      {!preview && !error && (
        <p className="m-0 text-xs text-muted">Checking what it brings…</p>
      )}
      {preview && (
        <>
          {added[0] && (
            <p className="m-0 text-xs wrap-anywhere text-muted">
              It goes to <span className="font-mono">{added[0].path}</span>.
            </p>
          )}
          <h3 className="mt-4 mb-1.5 text-[10px] font-semibold tracking-widest text-muted uppercase">
            Comes with it
          </h3>
          {brought.length === 0 ? (
            <p className="m-0 text-xs text-muted">
              Nothing else: everything it uses is built in or already there.
            </p>
          ) : (
            <ul
              aria-label="Comes with it"
              className="m-0 list-disc pl-[18px] text-xs leading-relaxed wrap-anywhere"
            >
              {brought.map((entry) => (
                <li key={`${entry.kind}:${entry.name}`}>
                  {kindLabels[entry.kind]} <b>{entry.name}</b>, to{" "}
                  <span className="font-mono">{entry.path}</span>
                </li>
              ))}
            </ul>
          )}
          {(preview.notes ?? []).length > 0 && (
            <Notice role="note" aria-label="What else changes">
              <ul className="m-0 list-disc pl-[18px] wrap-anywhere">
                {(preview.notes ?? []).map((note) => (
                  <li key={note}>{note}</li>
                ))}
              </ul>
            </Notice>
          )}
        </>
      )}
      <div className="mt-6 flex justify-end gap-2">
        <Button disabled={busy} onClick={onBack}>
          Back
        </Button>
        <Button
          variant="primary"
          disabled={busy || !preview}
          onClick={() => void add()}
        >
          {busy ? "Adding…" : `Add ${item.name}`}
        </Button>
      </div>
    </section>
  );
}

/**
 * One definition source as Go read it: when it could not be reached, and
 * each definition it shares that is not offered here, with Go's reason.
 */
function SourceNotice({ report }: { report: SourceReport }) {
  const withheld = (report.definitions ?? []).filter(
    (definition) => definition.status !== "offered",
  );
  if (!report.unreachable && withheld.length === 0) return null;
  const refuses = withheld.some(
    (definition) => definition.status === "refuses",
  );
  const body = (
    <>
      {report.unreachable && (
        <p className="mt-0 mb-1.5">
          It could not be reached ({report.unreachable})
          {report.fetchedAt
            ? `, so this is its snapshot from ${report.fetchedAt}.`
            : ", and nothing it shared is kept for this project."}
        </p>
      )}
      {withheld.length > 0 && (
        <ul className="m-0 list-disc pl-[18px] wrap-anywhere">
          {withheld.map((definition) => (
            <li
              key={`${definition.kind}:${definition.name}:${definition.mode}`}
            >
              {definition.kind} <b>{definition.name}</b> ({definition.mode}
              {definition.scope ? `, ${definition.scope}` : ""}):{" "}
              {definition.reason}
            </li>
          ))}
        </ul>
      )}
    </>
  );
  const title = `Definition source ${report.source}`;
  return refuses ? (
    <Alert>
      <p className="mt-0 mb-1.5">
        <b>{title}</b> shares what this build cannot apply, so plans in this
        project are refused.
      </p>
      {body}
    </Alert>
  ) : (
    <Notice role="note" title={title} className="mt-0 mb-4">
      {body}
    </Notice>
  );
}

/** What an add from the catalog did, as Go reported it. */
export function CatalogAddedNotice({ result }: { result: CatalogResult }) {
  const added = result.added ?? [];
  return (
    <Notice role="status" title={`Added ${result.item} from the catalog`}>
      <ul className="m-0 list-disc pl-[18px] wrap-anywhere">
        {added.map((entry) => (
          <li key={`${entry.kind}:${entry.name}`}>
            {kindLabels[entry.kind]} <b className="text-ink">{entry.name}</b> in{" "}
            <span className="font-mono">{entry.path}</span>
          </li>
        ))}
      </ul>
      {(result.notes ?? []).map((note) => (
        <p key={note} className="mt-2 mb-0">
          {note}
        </p>
      ))}
    </Notice>
  );
}
