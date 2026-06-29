<script lang="ts">
  import { api, friendlyError, type DiscoveredCCU, type SetupPayload } from "$lib/api/client";
  import { setupStore } from "$lib/stores/setup.svelte";
  import { setLocale, setTheme } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import BrandMark from "$lib/components/ui/BrandMark.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import { t } from "$lib/i18n";

  // The wizard keeps all accumulated state client-side and finalizes with a
  // single atomic POST /api/v1/setup. Mirrors the four steps of the former
  // server-rendered wizard: admin → locale → ccu → mqtt.
  const TOTAL = 4;
  let step = $state(1);
  let submitting = $state(false);

  // Step 1 — admin
  let username = $state("");
  let password = $state("");
  let confirm = $state("");

  // Step 2 — locale / theme
  let locale = $state<"de" | "en">("en");
  let theme = $state<"light" | "dark" | "system">("system");

  // Step 3 — CCU (optional)
  let ccuEnabled = $state(true);
  let ccuName = $state("");
  let ccuHost = $state("");
  let discoveredCCUs = $state<DiscoveredCCU[]>([]);
  let ccuUsername = $state("");
  let ccuPassword = $state("");
  const CCU_INTERFACES = [
    "HmIP-RF",
    "BidCos-RF",
    "BidCos-Wired",
    "HmIP-Wired",
    "VirtualDevices",
    "CUxD",
  ];
  let ccuInterfaces = $state<string[]>([]);

  // Step 4 — MQTT (optional)
  let mqttEnabled = $state(false);
  let mqttBroker = $state("");
  let mqttUsername = $state("");
  let mqttPassword = $state("");

  $effect(() => {
    if (step === 3 && ccuEnabled) {
      api.listDiscoveredCentrals().then((list) => {
        discoveredCCUs = list;
      }).catch(() => {
        // Silently ignore — discovery is best-effort in the wizard
      });
    }
  });

  function prefillCCU(ccu: DiscoveredCCU) {
    ccuName = ccu.name;
    ccuHost = ccu.host;
  }

  function toggleInterface(name: string, on: boolean) {
    ccuInterfaces = on
      ? [...ccuInterfaces, name]
      : ccuInterfaces.filter((i) => i !== name);
  }

  // Apply the chosen locale/theme live so the wizard immediately reflects the
  // selection — the same preference is persisted server-side on finalize.
  function onLocaleChange(v: string) {
    locale = v === "de" ? "de" : "en";
    setLocale(locale);
  }
  function onThemeChange(v: string) {
    theme = v === "light" || v === "dark" ? v : "system";
    setTheme(theme);
  }

  const adminValid = $derived(
    username.trim() !== "" && password.length >= 8 && password === confirm,
  );
  const ccuValid = $derived(
    !ccuEnabled ||
      (ccuName.trim() !== "" &&
        ccuHost.trim() !== "" &&
        ccuInterfaces.length > 0),
  );
  const mqttValid = $derived(!mqttEnabled || mqttBroker.trim() !== "");

  const canAdvance = $derived(
    (step === 1 && adminValid) ||
      (step === 2) ||
      (step === 3 && ccuValid) ||
      (step === 4 && mqttValid),
  );

  function next() {
    if (step < TOTAL && canAdvance) step += 1;
  }
  function back() {
    if (step > 1) step -= 1;
  }

  async function finish() {
    if (!adminValid || !ccuValid || !mqttValid) return;
    submitting = true;
    try {
      const payload: SetupPayload = {
        admin: { username: username.trim(), password },
        locale: { locale, theme },
      };
      if (ccuEnabled) {
        payload.ccu = {
          name: ccuName.trim(),
          host: ccuHost.trim(),
          interfaces: ccuInterfaces,
        };
        if (ccuUsername.trim()) payload.ccu.username = ccuUsername.trim();
        if (ccuPassword) payload.ccu.password = ccuPassword;
      }
      if (mqttEnabled) {
        payload.mqtt = { broker_url: mqttBroker.trim() };
        if (mqttUsername.trim()) payload.mqtt.username = mqttUsername.trim();
        if (mqttPassword) payload.mqtt.password = mqttPassword;
      }
      await api.submitSetup(payload);
      toastStore.success(t("setup.done.title"), t("setup.done.detail"));
      // Hand off to the login screen; App.svelte renders it now that setup is
      // no longer required and no session exists yet.
      setupStore.complete();
    } catch (err) {
      toastStore.error(t("setup.error.title"), friendlyError(err, t));
    } finally {
      submitting = false;
    }
  }
</script>

<section class="flex min-h-screen items-center justify-center px-4 py-8">
  <div
    class="w-full max-w-lg rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900"
  >
    <div class="mb-4 flex items-center justify-center">
      <BrandMark mode="wordmark" height={40} />
    </div>

    <!-- Step progress -->
    <div class="mb-6 flex items-center justify-center gap-2" aria-hidden="true">
      {#each Array.from({ length: TOTAL }, (_, i) => i + 1) as n (n)}
        <span
          class="h-2 w-2 rounded-full transition-colors {n === step
            ? 'bg-brand-500'
            : n < step
              ? 'bg-brand-500/50'
              : 'bg-slate-300 dark:bg-slate-700'}"
        ></span>
      {/each}
    </div>
    <p class="mb-4 text-center text-sm text-slate-500 dark:text-slate-400">
      {t("setup.step.progress", { current: step, total: TOTAL })}
    </p>

    {#if step === 1}
      <h1 class="mb-4 text-lg font-semibold">{t("setup.step1.title")}</h1>
      <label class="mb-3 block">
        <span class="mb-1 block text-sm font-medium">{t("setup.username")}</span>
        <Input type="text" autocomplete="username" bind:value={username} />
      </label>
      <label class="mb-3 block">
        <span class="mb-1 block text-sm font-medium">{t("setup.password")}</span>
        <Input type="password" autocomplete="new-password" bind:value={password} />
      </label>
      <label class="mb-1 block">
        <span class="mb-1 block text-sm font-medium">{t("setup.confirm")}</span>
        <Input type="password" autocomplete="new-password" bind:value={confirm} />
      </label>
      {#if password && password.length < 8}
        <p class="text-xs text-red-600 dark:text-red-400">{t("setup.password.too_short")}</p>
      {:else if confirm && password !== confirm}
        <p class="text-xs text-red-600 dark:text-red-400">{t("setup.password.mismatch")}</p>
      {/if}
    {:else if step === 2}
      <h1 class="mb-4 text-lg font-semibold">{t("setup.step2.title")}</h1>
      <label class="mb-3 block">
        <span class="mb-1 block text-sm font-medium">{t("setup.locale.label")}</span>
        <Select
          value={locale}
          onValueChange={onLocaleChange}
          options={[
            { value: "en", label: "English" },
            { value: "de", label: "Deutsch" },
          ]}
        />
      </label>
      <label class="mb-1 block">
        <span class="mb-1 block text-sm font-medium">{t("setup.theme.label")}</span>
        <Select
          value={theme}
          onValueChange={onThemeChange}
          options={[
            { value: "system", label: t("setup.theme.system") },
            { value: "light", label: t("setup.theme.light") },
            { value: "dark", label: t("setup.theme.dark") },
          ]}
        />
      </label>
    {:else if step === 3}
      <h1 class="mb-2 text-lg font-semibold">{t("setup.step3.title")}</h1>
      <label class="mb-4 flex items-center gap-3">
        <Switch checked={ccuEnabled} onCheckedChange={(v) => (ccuEnabled = v)} />
        <span class="text-sm">{t("setup.ccu.enable")}</span>
      </label>
      {#if ccuEnabled}
        {#if discoveredCCUs.length > 0}
          <div class="mb-3">
            <p class="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">{t("discovery.found_hint")}</p>
            <div class="flex flex-wrap gap-2">
              {#each discoveredCCUs as ccu (ccu.serial)}
                <button
                  type="button"
                  class="flex items-center gap-1 rounded border border-slate-300 px-2 py-1 text-xs hover:bg-slate-50 dark:border-slate-600 dark:hover:bg-slate-800"
                  onclick={() => prefillCCU(ccu)}
                >
                  <span class="font-medium">{ccu.name}</span>
                  <span class="text-slate-400">{ccu.host}</span>
                </button>
              {/each}
            </div>
          </div>
        {/if}
        <label class="mb-3 block">
          <span class="mb-1 block text-sm font-medium">{t("setup.ccu.name")}</span>
          <Input type="text" bind:value={ccuName} />
        </label>
        <label class="mb-3 block">
          <span class="mb-1 block text-sm font-medium">{t("setup.ccu.host")}</span>
          <Input type="text" placeholder="192.168.0.10" bind:value={ccuHost} />
        </label>
        <div class="mb-3 grid grid-cols-2 gap-3">
          <label class="block">
            <span class="mb-1 block text-sm font-medium">{t("setup.username")}</span>
            <Input type="text" autocomplete="off" bind:value={ccuUsername} />
          </label>
          <label class="block">
            <span class="mb-1 block text-sm font-medium">{t("setup.password")}</span>
            <Input type="password" autocomplete="off" bind:value={ccuPassword} />
          </label>
        </div>
        <fieldset class="mb-1">
          <legend class="mb-1 text-sm font-medium">{t("setup.ccu.interfaces")}</legend>
          <p class="mb-2 text-xs text-slate-500 dark:text-slate-400">{t("setup.ccu.interfaces_hint")}</p>
          <div class="grid grid-cols-2 gap-1">
            {#each CCU_INTERFACES as iface (iface)}
              <label class="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-slate-300 text-brand-500 focus:ring-brand-500 dark:border-slate-600 dark:bg-slate-800"
                  checked={ccuInterfaces.includes(iface)}
                  onchange={(e) => toggleInterface(iface, e.currentTarget.checked)}
                />
                {iface}
              </label>
            {/each}
          </div>
        </fieldset>
      {/if}
    {:else if step === 4}
      <h1 class="mb-2 text-lg font-semibold">{t("setup.step4.title")}</h1>
      <label class="mb-4 flex items-center gap-3">
        <Switch checked={mqttEnabled} onCheckedChange={(v) => (mqttEnabled = v)} />
        <span class="text-sm">{t("setup.mqtt.enable")}</span>
      </label>
      {#if mqttEnabled}
        <label class="mb-3 block">
          <span class="mb-1 block text-sm font-medium">{t("setup.mqtt.broker")}</span>
          <Input type="text" placeholder="mqtt://192.168.0.5:1883" bind:value={mqttBroker} />
        </label>
        <div class="mb-1 grid grid-cols-2 gap-3">
          <label class="block">
            <span class="mb-1 block text-sm font-medium">{t("setup.username")}</span>
            <Input type="text" autocomplete="off" bind:value={mqttUsername} />
          </label>
          <label class="block">
            <span class="mb-1 block text-sm font-medium">{t("setup.password")}</span>
            <Input type="password" autocomplete="off" bind:value={mqttPassword} />
          </label>
        </div>
      {/if}
    {/if}

    <div class="mt-6 flex items-center justify-between gap-2">
      <Button variant="ghost" onclick={back} disabled={step === 1 || submitting}>
        {t("setup.back")}
      </Button>
      {#if step < TOTAL}
        <Button onclick={next} disabled={!canAdvance || submitting}>
          {t("setup.next")}
        </Button>
      {:else}
        <Button onclick={finish} disabled={!adminValid || !ccuValid || !mqttValid || submitting}>
          {submitting ? t("setup.finishing") : t("setup.finish")}
        </Button>
      {/if}
    </div>
  </div>
</section>
