// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// The room and function catalogues the first-time configuration of an
// accepted device picks from. One instance per surface: the new-devices
// view's accept dialog and the add-device dialog each hold one, so a
// dialog listing several devices loads the catalogues once.
import { api, ApiError } from "$lib/api/client";
import { toastStore } from "$lib/stores/toast.svelte";
import { t } from "$lib/i18n";

const byName = (a: string, b: string) => a.localeCompare(b);

export type RoomFunctionCatalog = ReturnType<typeof createRoomFunctionCatalog>;

export function createRoomFunctionCatalog() {
  let rooms = $state<string[]>([]);
  let functions = $state<string[]>([]);
  let loaded = false;
  let inFlight: Promise<void> | null = null;

  // Loaded lazily, once. A failure leaves the lists empty and says so: the
  // operator can still accept and name the device, and the next open of
  // the surface tries again.
  function load(): Promise<void> {
    if (loaded) return Promise.resolve();
    if (inFlight) return inFlight;
    inFlight = (async () => {
      try {
        const [r, f] = await Promise.all([api.listRooms(), api.listFunctions()]);
        rooms = r.map((x) => x.name).sort(byName);
        functions = f.map((x) => x.name).sort(byName);
        loaded = true;
      } catch (err) {
        toastStore.error(
          err instanceof ApiError
            ? `${err.status}: ${t("inbox.accept_dialog.catalog_error")}`
            : t("inbox.accept_dialog.catalog_error"),
        );
      } finally {
        inFlight = null;
      }
    })();
    return inFlight;
  }

  // The combobox may create a brand-new room / function on the spot. The
  // system refuses the write on a duplicate name, a missing permission or
  // an unreachable backend, and the combobox discards the returned
  // promise, so a rejection that is not reported here reaches nothing at
  // all — the chip never appears and the operator is told nothing.
  async function createRoom(name: string, central: string | undefined) {
    try {
      await api.createRoom(name, central);
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
      throw err;
    }
    if (!rooms.includes(name)) rooms = [...rooms, name].sort(byName);
    toastStore.success(t("roomfn.created.room"));
  }

  async function createFunction(name: string, central: string | undefined) {
    try {
      await api.createFunction(name, central);
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
      throw err;
    }
    if (!functions.includes(name)) functions = [...functions, name].sort(byName);
    toastStore.success(t("roomfn.created.function"));
  }

  return {
    get rooms() {
      return rooms;
    },
    get functions() {
      return functions;
    },
    load,
    createRoom,
    createFunction,
  };
}
