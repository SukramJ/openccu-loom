<script lang="ts">
  import { api, ApiError } from "$lib/api/client";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";

  // Runtime TLS certificate replacement. Uploading a PEM cert + key
  // hot-reloads the listener so the API and SPA (same port) are
  // re-secured without a restart. Admin-gated server-side; a 503 means
  // TLS is not enabled (north.rest.tls_cert_file/tls_key_file unset).

  let certFile = $state<File | null>(null);
  let keyFile = $state<File | null>(null);
  let busy = $state(false);

  const canSubmit = $derived(certFile !== null && keyFile !== null && !busy);

  function pickCert(e: Event) {
    certFile = (e.target as HTMLInputElement).files?.[0] ?? null;
  }
  function pickKey(e: Event) {
    keyFile = (e.target as HTMLInputElement).files?.[0] ?? null;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!certFile || !keyFile) return;
    busy = true;
    try {
      await api.uploadTLSCertificate(certFile, keyFile);
      toastStore.success(t("tls.uploaded"));
      certFile = null;
      keyFile = null;
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        toastStore.error(t("tls.not_enabled"));
      } else {
        toastStore.error(
          err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
        );
      }
    } finally {
      busy = false;
    }
  }
</script>

<Card class="p-4">
  <header class="mb-3">
    <h3 class="text-base font-semibold">{t("tls.title")}</h3>
    <p class="text-xs text-[var(--ha-secondary-text-color)]">
      {t("tls.subtitle")}
    </p>
  </header>

  <form class="max-w-md space-y-3" onsubmit={submit}>
    <label class="block text-xs">
      <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
        >{t("tls.cert_label")}</span
      >
      <input
        type="file"
        accept=".pem,.crt,.cert,application/x-pem-file"
        onchange={pickCert}
        disabled={busy}
        class="block w-full text-sm"
      />
    </label>
    <label class="block text-xs">
      <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
        >{t("tls.key_label")}</span
      >
      <input
        type="file"
        accept=".pem,.key,application/x-pem-file"
        onchange={pickKey}
        disabled={busy}
        class="block w-full text-sm"
      />
    </label>
    <Button type="submit" disabled={!canSubmit}>
      {t("tls.upload")}
    </Button>
  </form>
</Card>
