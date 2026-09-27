import { api, type TaxonomyCentral, type TaxonomyEnum } from "$lib/api/client";

/**
 * Every central's taxonomy trees (`GET /taxonomy`). Views that pick or
 * edit nodes by path read it here; an edit reloads it, since a rename or
 * move changes the paths below the node.
 */
function createTaxonomyStore() {
  let centrals = $state<TaxonomyCentral[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);

  async function refresh() {
    loading = true;
    error = null;
    try {
      centrals = (await api.getTaxonomy()).centrals;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function central(name: string | undefined): TaxonomyCentral | undefined {
    return name ? centrals.find((c) => c.central === name) : undefined;
  }

  function enumOf(centralName: string | undefined, enumId: string): TaxonomyEnum | undefined {
    return central(centralName)?.enums.find((e) => e.id === enumId);
  }

  return {
    get centrals() {
      return centrals;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    /** The centrals whose taxonomies nest — the ones edited as trees. */
    get trees() {
      return centrals.filter((c) => c.tree);
    },
    refresh,
    central,
    enumOf,
  };
}

export const taxonomyStore = createTaxonomyStore();
