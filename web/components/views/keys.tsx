"use client";

import { useState } from "react";
import { useProjects } from "@/components/filters";
import { IconAlert, IconEdit, IconKey, IconPlus } from "@/components/icons";
import { Modal } from "@/components/overlay";
import { Badge, Button, CopyButton, EmptyState, ErrorState, Field, IconButton, Input, Meter, PageHeader, Panel, Skeleton, cx } from "@/components/ui";
import { ApiError, type ApiKey } from "@/lib/api";
import { formatAgo, formatDateTime, formatInt, formatUsd } from "@/lib/format";
import { useKeys } from "@/lib/hooks";
import { type FormErrors, type KeyForm, createPayload, emptyForm, formFromKey, updatePayload, validateForm } from "@/lib/keys";
import { useClient } from "@/lib/session";

export function KeysView() {
  const keys = useKeys();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<ApiKey | null>(null);
  const list = keys.data ?? [];
  const active = list.filter((k) => !k.disabled).length;

  return (
    <>
      <PageHeader
        title="API keys"
        description="Virtual keys clients use to call the gateway. Limits and budgets apply per key; changes take effect immediately."
        meta={
          keys.data ? (
            <span className="font-mono text-[11px] text-faint">
              {list.length} key{list.length === 1 ? "" : "s"} · {active} active
            </span>
          ) : null
        }
        actions={
          <Button variant="primary" icon={<IconPlus size={14} />} onClick={() => setCreating(true)} disabled={!!keys.error && !keys.data}>
            Create key
          </Button>
        }
      />

      <Panel>
        {keys.error && !keys.data ? (
          <ErrorState error={keys.error} onRetry={() => keys.mutate()} />
        ) : !keys.data ? (
          <KeysSkeleton />
        ) : list.length === 0 ? (
          <EmptyState
            icon={<IconKey size={18} />}
            title="No API keys yet"
            action={
              <Button variant="primary" icon={<IconPlus size={14} />} onClick={() => setCreating(true)}>
                Create your first key
              </Button>
            }
          >
            Create a key, then call <code className="font-mono text-fg-2">/v1/chat/completions</code> with it as a Bearer token.
          </EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[1020px] text-[12.5px]">
              <caption className="sr-only">API keys</caption>
              <thead>
                <tr className="text-left font-mono text-[10.5px] uppercase tracking-[0.06em] text-faint">
                  <th scope="col" className="py-2.5 pl-4 pr-2 font-normal">
                    Key
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Project
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Models
                  </th>
                  <th scope="col" className="px-2 text-right font-normal">
                    RPM
                  </th>
                  <th scope="col" className="px-2 text-right font-normal">
                    TPM
                  </th>
                  <th scope="col" className="w-[200px] px-3 font-normal">
                    Budget this month
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Last used
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Enabled
                  </th>
                  <th scope="col" className="py-2.5 pl-2 pr-4 font-normal">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody className="tnum">
                {list.map((k) => (
                  <KeyRow key={k.id} k={k} onEdit={() => setEditing(k)} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <CreateKeyDialog open={creating} onClose={() => setCreating(false)} />
      <EditKeyDialog k={editing} onClose={() => setEditing(null)} />
    </>
  );
}

function KeyRow({ k, onEdit }: { k: ApiKey; onEdit: () => void }) {
  const spent = k.spent_this_month_usd;
  return (
    <tr className={cx("border-t border-line transition-colors hover:bg-hover/40", k.disabled && "opacity-55")}>
      <td className="py-2.5 pl-4 pr-2">
        <div className="font-medium text-fg">{k.name}</div>
        <div className="mt-0.5 font-mono text-[11px] text-muted" title={`id ${k.id}`}>
          {k.prefix}
          <span className="text-faint">…</span>
        </div>
      </td>
      <td className="px-2 py-2.5 text-fg-2">{k.project}</td>
      <td className="max-w-[220px] px-2 py-2.5">
        {k.allowed_models.length === 0 ? (
          <span className="text-[12px] text-muted">All models</span>
        ) : (
          <div className="flex flex-wrap gap-1">
            {k.allowed_models.slice(0, 3).map((m) => (
              <Badge key={m}>{m}</Badge>
            ))}
            {k.allowed_models.length > 3 ? (
              <Badge title={k.allowed_models.slice(3).join(", ")}>+{k.allowed_models.length - 3}</Badge>
            ) : null}
          </div>
        )}
      </td>
      <td className="px-2 py-2.5 text-right font-mono text-fg-2">{k.rpm_limit === null ? <Unlimited /> : formatInt(k.rpm_limit)}</td>
      <td className="px-2 py-2.5 text-right font-mono text-fg-2">{k.tpm_limit === null ? <Unlimited /> : formatInt(k.tpm_limit)}</td>
      <td className="px-3 py-2.5">
        {k.monthly_budget_usd === null ? (
          <div className="font-mono text-[11.5px] text-muted">
            {spent !== null ? formatUsd(spent) : "—"} <span className="text-faint">· no budget</span>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            <div className="flex items-baseline justify-between font-mono text-[11.5px]">
              <span className="text-fg">{spent !== null ? formatUsd(spent) : "—"}</span>
              <span className="text-muted">of {formatUsd(k.monthly_budget_usd)}</span>
            </div>
            <Meter value={spent ?? 0} max={k.monthly_budget_usd} label={`Budget used by ${k.name}`} />
          </div>
        )}
      </td>
      <td className="whitespace-nowrap px-2 py-2.5 text-[12px] text-fg-2" title={k.last_used_at ? formatDateTime(k.last_used_at) : "Never used"}>
        {k.last_used_at ? formatAgo(k.last_used_at) : <span className="text-faint">never</span>}
      </td>
      <td className="px-2 py-2.5">
        <DisableToggle k={k} />
      </td>
      <td className="py-2.5 pl-2 pr-4 text-right">
        <IconButton label={`Edit ${k.name}`} onClick={onEdit}>
          <IconEdit size={14} />
        </IconButton>
      </td>
    </tr>
  );
}

function Unlimited() {
  return <span className="text-faint">∞</span>;
}

function DisableToggle({ k }: { k: ApiKey }) {
  const client = useClient();
  const keys = useKeys();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const on = !k.disabled;
  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label={`${on ? "Disable" : "Enable"} ${k.name}`}
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setErr(null);
          try {
            const updated = await client.updateKey(k.id, { disabled: on });
            await keys.mutate((cur) => cur?.map((x) => (x.id === k.id ? updated : x)), { revalidate: false });
          } catch (e) {
            setErr(e instanceof Error ? e.message : "failed");
          } finally {
            setBusy(false);
          }
        }}
        className={cx(
          "relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors duration-200 disabled:opacity-60",
          on ? "border-transparent bg-ok" : "border-line-strong bg-panel-2",
        )}
      >
        <span
          className={cx(
            "inline-block size-3.5 rounded-full bg-white shadow transition-transform duration-200",
            on ? "translate-x-[18px]" : "translate-x-[2px]",
          )}
        />
      </button>
      {err ? (
        <span className="text-err" title={err}>
          <IconAlert size={13} />
        </span>
      ) : null}
    </div>
  );
}

// -------------------------------------------------------------- dialogs

function KeyFields({
  form,
  set,
  errors,
  mode,
  projects,
}: {
  form: KeyForm;
  set: (patch: Partial<KeyForm>) => void;
  errors: FormErrors;
  mode: "create" | "edit";
  projects: string[];
}) {
  return (
    <div className="grid grid-cols-2 gap-x-3 gap-y-4">
      <Field label="Name" htmlFor="k-name" className="col-span-2 sm:col-span-1" hint={errors.name ? <Err>{errors.name}</Err> : undefined}>
        <Input
          id="k-name"
          autoFocus
          value={form.name}
          onChange={(e) => set({ name: e.target.value })}
          placeholder="checkout-service"
          aria-invalid={!!errors.name}
        />
      </Field>
      <Field
        label="Project"
        htmlFor="k-project"
        className="col-span-2 sm:col-span-1"
        hint={mode === "edit" ? "Fixed after creation." : "Created if it doesn't exist."}
      >
        <Input
          id="k-project"
          list="k-projects"
          value={form.project}
          onChange={(e) => set({ project: e.target.value })}
          disabled={mode === "edit"}
          placeholder="default"
        />
        <datalist id="k-projects">
          {projects.map((p) => (
            <option key={p} value={p} />
          ))}
        </datalist>
      </Field>
      <Field label="Allowed models" htmlFor="k-models" className="col-span-2" hint="Model aliases, comma-separated. Leave empty to allow every model.">
        <Input
          id="k-models"
          value={form.allowedModels}
          onChange={(e) => set({ allowedModels: e.target.value })}
          placeholder="All models"
          className="font-mono"
        />
      </Field>
      <Field label="Requests / min" htmlFor="k-rpm" hint={errors.rpm ? <Err>{errors.rpm}</Err> : "Empty = unlimited"}>
        <Input
          id="k-rpm"
          inputMode="numeric"
          value={form.rpm}
          onChange={(e) => set({ rpm: e.target.value })}
          placeholder="∞"
          className="tnum font-mono"
          aria-invalid={!!errors.rpm}
        />
      </Field>
      <Field label="Tokens / min" htmlFor="k-tpm" hint={errors.tpm ? <Err>{errors.tpm}</Err> : "Empty = unlimited"}>
        <Input
          id="k-tpm"
          inputMode="numeric"
          value={form.tpm}
          onChange={(e) => set({ tpm: e.target.value })}
          placeholder="∞"
          className="tnum font-mono"
          aria-invalid={!!errors.tpm}
        />
      </Field>
      <Field label="Monthly budget (USD)" htmlFor="k-budget" className="col-span-2" hint={errors.budget ? <Err>{errors.budget}</Err> : "Requests get 402 once spend reaches the budget. Empty = no budget."}>
        <div className="relative">
          <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 font-mono text-[13px] text-muted">$</span>
          <Input
            id="k-budget"
            inputMode="decimal"
            value={form.budget}
            onChange={(e) => set({ budget: e.target.value })}
            placeholder="no budget"
            className="tnum pl-6 font-mono"
            aria-invalid={!!errors.budget}
          />
        </div>
      </Field>
    </div>
  );
}

function Err({ children }: { children: React.ReactNode }) {
  return <span className="text-err">{children}</span>;
}

function CreateKeyDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const client = useClient();
  const keys = useKeys();
  const { projects } = useProjects();
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const [errors, setErrors] = useState<FormErrors>({});
  const [busy, setBusy] = useState(false);
  const [apiErr, setApiErr] = useState<string | null>(null);
  const [created, setCreated] = useState<{ secret: string; key: ApiKey } | null>(null);

  const close = () => {
    onClose();
    // Reset after the close animation; the secret is dropped from memory.
    setTimeout(() => {
      setForm(emptyForm());
      setErrors({});
      setApiErr(null);
      setCreated(null);
    }, 200);
  };

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const v = validateForm(form);
    setErrors(v);
    if (Object.keys(v).length) return;
    setBusy(true);
    setApiErr(null);
    try {
      const res = await client.createKey(createPayload(form));
      setCreated(res);
      await keys.mutate();
    } catch (err) {
      setApiErr(err instanceof ApiError ? err.message : "Couldn't create the key.");
    } finally {
      setBusy(false);
    }
  }

  if (created) {
    return (
      <Modal
        open={open}
        onClose={close}
        title="Key created"
        description={`${created.key.name} · ${created.key.project}`}
        footer={
          <Button variant="primary" onClick={close}>
            I&apos;ve saved it
          </Button>
        }
      >
        <div className="flex gap-2.5 rounded-lg border border-[color:var(--warn)]/30 bg-warn-dim p-3 text-[12.5px] text-fg">
          <IconAlert size={16} className="mt-0.5 shrink-0 text-warn" />
          <p>
            <strong className="font-semibold">Copy this secret now. You won&apos;t see it again.</strong> Switchyard stores only a hash; if it&apos;s lost,
            create a new key.
          </p>
        </div>
        <div className="mt-4 flex items-center gap-2 rounded-lg border border-line-strong bg-bg p-1.5 pl-3">
          <code className="min-w-0 flex-1 break-all font-mono text-[12.5px] text-signal" data-testid="key-secret">
            {created.secret}
          </code>
          <CopyButton value={created.secret} />
        </div>
        <pre className="mt-4 overflow-x-auto rounded-lg border border-line bg-panel p-3 font-mono text-[11.5px] leading-relaxed text-fg-2">
          {`curl $SWITCHYARD_URL/v1/chat/completions \\
  -H "Authorization: Bearer ${created.key.prefix}…" \\
  -d '{"model":"…","messages":[…]}'`}
        </pre>
      </Modal>
    );
  }

  return (
    <Modal
      open={open}
      onClose={close}
      title="Create API key"
      description="The secret is shown once, right after creation."
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="create-key" disabled={busy}>
            {busy ? "Creating…" : "Create key"}
          </Button>
        </>
      }
    >
      <form id="create-key" onSubmit={submit} noValidate>
        <KeyFields
          form={form}
          set={(p) => setForm((f) => ({ ...f, ...p }))}
          errors={errors}
          mode="create"
          projects={projects.map((p) => p.name)}
        />
        {apiErr ? (
          <p role="alert" className="mt-4 text-[12px] text-err">
            {apiErr}
          </p>
        ) : null}
      </form>
    </Modal>
  );
}

function EditKeyDialog({ k, onClose }: { k: ApiKey | null; onClose: () => void }) {
  return k ? <EditKeyForm key={k.id} k={k} onClose={onClose} /> : <Modal open={false} onClose={onClose} title="">{null}</Modal>;
}

function EditKeyForm({ k, onClose }: { k: ApiKey; onClose: () => void }) {
  const client = useClient();
  const keys = useKeys();
  const [form, setForm] = useState<KeyForm>(() => formFromKey(k));
  const [errors, setErrors] = useState<FormErrors>({});
  const [busy, setBusy] = useState(false);
  const [apiErr, setApiErr] = useState<string | null>(null);
  const patch = Object.keys(validateForm(form)).length ? {} : updatePayload(k, form);
  const dirty = Object.keys(patch).length > 0;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const v = validateForm(form);
    setErrors(v);
    if (Object.keys(v).length) return;
    if (!dirty) return onClose();
    setBusy(true);
    setApiErr(null);
    try {
      const updated = await client.updateKey(k.id, updatePayload(k, form));
      await keys.mutate((cur) => cur?.map((x) => (x.id === k.id ? updated : x)), { revalidate: true });
      onClose();
    } catch (err) {
      setApiErr(err instanceof ApiError ? err.message : "Couldn't save changes.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={`Edit ${k.name}`}
      description={
        <span className="font-mono">
          {k.prefix}… · created {formatAgo(k.created_at)}
        </span>
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="edit-key" disabled={busy || !dirty}>
            {busy ? "Saving…" : "Save changes"}
          </Button>
        </>
      }
    >
      <form id="edit-key" onSubmit={submit} noValidate>
        <KeyFields form={form} set={(p) => setForm((f) => ({ ...f, ...p }))} errors={errors} mode="edit" projects={[]} />
        {apiErr ? (
          <p role="alert" className="mt-4 text-[12px] text-err">
            {apiErr}
          </p>
        ) : null}
      </form>
    </Modal>
  );
}

function KeysSkeleton() {
  return (
    <div aria-busy="true">
      {Array.from({ length: 5 }, (_, i) => (
        <div key={i} className="flex items-center gap-6 border-t border-line px-4 py-3.5 first:border-t-0">
          <div className="flex w-40 flex-col gap-1.5">
            <Skeleton className="h-3.5 w-28" />
            <Skeleton className="h-3 w-20" />
          </div>
          <Skeleton className="h-3.5 w-16" />
          <Skeleton className="h-5 w-24" />
          <Skeleton className="ml-auto h-3.5 w-12" />
          <Skeleton className="h-2 w-44" />
          <Skeleton className="h-5 w-9 rounded-full" />
        </div>
      ))}
    </div>
  );
}
