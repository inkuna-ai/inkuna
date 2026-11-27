import { useState } from "react";
import type { SecretRef, SecretStatus } from "../../types";
import {
  Button,
  Chip,
  Modal,
  TableScroll,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import {
  FieldErrors,
  LinkButton,
  SetupPanel,
  formGridClass,
  type UpdateConfig,
} from "./shared";

export function SecretsPanel({
  secrets,
  declared,
  saving,
  update,
  onStore,
  onRemove,
  onForget,
}: {
  secrets: SecretStatus[];
  /** The declarations in inkuna.yaml; description and required are edited here. */
  declared: SecretRef[];
  saving: boolean;
  update: UpdateConfig;
  /** Resolves true once the value is in the keychain. */
  onStore(name: string, value: string): Promise<boolean>;
  onRemove(name: string): void;
  /** Removes the value, the declaration, and runtime exports together. */
  onForget(name: string): void;
}) {
  const [secretName, setSecretName] = useState("");
  const [secretValue, setSecretValue] = useState("");
  // Forgetting deletes the keychain value too, so it asks first.
  const [forgetting, setForgetting] = useState<string | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    const name = secretName.trim();
    const value = secretValue;
    // Drop the value from the form before the round trip so it is never
    // rendered again, whatever the outcome.
    setSecretValue("");
    if (await onStore(name, value)) setSecretName("");
  }

  return (
    <SetupPanel
      title="Secrets"
      wide
      description="Values live in the OS keychain. inkuna.yaml only lists the names; the environment overrides the keychain for one launch."
    >
      <FieldErrors field="secrets" />
      <TableScroll>
        <table className={tableClass}>
          <caption className="pb-2 text-left text-[11px] text-muted">
            Declared secrets
          </caption>
          <thead>
            <tr>
              <th className={thClass}>Name</th>
              <th className={thClass}>Status</th>
              <th className={thClass}>Source</th>
              <th className={thClass}>Description</th>
              <th className={thClass}>Required</th>
              <th className={thClass}></th>
            </tr>
          </thead>
          <tbody>
            {secrets.map((secret) => (
              <tr key={secret.name}>
                <td className={tdClass}>
                  <code className="font-mono wrap-anywhere">{secret.name}</code>
                </td>
                <td className={tdClass}>
                  <Chip tone={secret.available ? "green" : "error"}>
                    {secret.available ? "available" : "missing"}
                  </Chip>
                </td>
                <td className={tdClass}>{secret.source}</td>
                <td className={`${tdClass} min-w-[160px]`}>
                  <input
                    className={`${inputClass} p-1.5 text-xs`}
                    aria-label={`Description of ${secret.name}`}
                    value={
                      declared.find((item) => item.name === secret.name)
                        ?.description ?? ""
                    }
                    onChange={(event) =>
                      update((draft) => {
                        const entry = draft.secrets.find(
                          (item) => item.name === secret.name,
                        );
                        if (entry)
                          entry.description = event.target.value || undefined;
                      })
                    }
                  />
                </td>
                <td className={tdClass}>
                  <input
                    type="checkbox"
                    aria-label={`${secret.name} is required`}
                    checked={
                      declared.find((item) => item.name === secret.name)
                        ?.required ?? false
                    }
                    onChange={(event) =>
                      update((draft) => {
                        const entry = draft.secrets.find(
                          (item) => item.name === secret.name,
                        );
                        if (entry)
                          entry.required = event.target.checked || undefined;
                      })
                    }
                  />
                </td>
                <td className={`${tdClass} text-right whitespace-nowrap`}>
                  <LinkButton
                    disabled={saving}
                    onClick={() => onRemove(secret.name)}
                  >
                    Remove value
                  </LinkButton>{" "}
                  <LinkButton
                    disabled={saving}
                    aria-label={`Forget ${secret.name}`}
                    onClick={() => setForgetting(secret.name)}
                  >
                    Forget
                  </LinkButton>
                </td>
              </tr>
            ))}
            {secrets.length === 0 && (
              <tr>
                <td colSpan={6} className={`${tdClass} text-muted`}>
                  No secrets declared.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </TableScroll>
      <form className={formGridClass} onSubmit={(event) => void submit(event)}>
        <input
          className={inputClass}
          aria-label="Secret name"
          placeholder="ANTHROPIC_API_KEY"
          value={secretName}
          onChange={(event) => setSecretName(event.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
        <input
          className={inputClass}
          aria-label="Secret value"
          type="password"
          placeholder="value (never shown again)"
          value={secretValue}
          onChange={(event) => setSecretValue(event.target.value)}
          autoComplete="off"
        />
        <Button
          type="submit"
          variant="primary"
          disabled={saving || !secretName.trim() || !secretValue}
        >
          Store in keychain
        </Button>
      </form>
      {forgetting && (
        <Modal
          title={`Forget ${forgetting}?`}
          onClose={() => setForgetting(null)}
        >
          <p className="m-0 text-sm wrap-anywhere text-muted">
            This deletes the value from the keychain, removes the declaration
            from inkuna.yaml, and stops exporting it to every runtime. Agents
            that list this secret will no longer save until it is unticked.
          </p>
          <div className="mt-6 flex justify-end gap-2">
            <Button onClick={() => setForgetting(null)}>Cancel</Button>
            <Button
              variant="danger"
              onClick={() => {
                onForget(forgetting);
                setForgetting(null);
              }}
            >
              Forget secret
            </Button>
          </div>
        </Modal>
      )}
    </SetupPanel>
  );
}
