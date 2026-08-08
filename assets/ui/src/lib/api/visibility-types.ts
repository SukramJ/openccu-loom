// Visibility / un_ignore REST DTOs. Hand-written mirror of the
// openapi.yaml shapes for the /visibility/unignore* endpoints.
// See notes/concepts/ui/unignore-concept.md.

export type UnIgnoreEntry = {
  pattern: string;
  updated_at?: string;
  updated_by?: string;
};

export type UnIgnoreCentralPatterns = {
  central_name: string;
  patterns: UnIgnoreEntry[];
};

export type UnIgnoreListResponse = {
  centrals: UnIgnoreCentralPatterns[];
};

export type UnIgnoreUpdateRequest = {
  central_name: string;
  patterns: string[];
};

export type UnIgnoreUpdateResponse = {
  applied_count: number;
  parse_errors?: string[];
  affected_devices: number;
  patterns: UnIgnoreEntry[];
};

export type UnIgnoreCandidateList = {
  candidates: string[];
  include_master: boolean;
};
