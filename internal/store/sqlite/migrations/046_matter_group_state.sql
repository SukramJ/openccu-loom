-- +goose Up
-- +goose StatementBegin

-- The Matter store schema is go-fabric's (store/schema.sql), carried here
-- as goose migrations because this daemon owns its one database. Four
-- tables that module added after migration 006 were missing, so the first
-- KeySetWrite of a commissioner failed and every bridged endpoint's Groups
-- cluster was unusable; tests/contract pins loom's schema against
-- go-fabric's table set from here on. The DDL is verbatim.

-- matter_group_table persists the group membership of this node's
-- endpoints per fabric — the GroupKeyManagement GroupTable attribute
-- (Matter §11.2.6.2, GroupInfoMapStruct) that the Groups cluster's
-- AddGroup / RemoveGroup maintain. endpoints_json is the ordered endpoint
-- list as a JSON array. Mirrors matter.js GroupKeyManagementServer state
-- `groupTable` (packages/node/src/behaviors/group-key-management/
-- GroupKeyManagementServer.ts addEndpointForGroup / removeEndpoint).
CREATE TABLE IF NOT EXISTS matter_group_table (
    fabric_index        INTEGER NOT NULL,
    group_id            INTEGER NOT NULL CHECK(group_id BETWEEN 1 AND 65535),
    group_name          TEXT    NOT NULL DEFAULT '',
    endpoints_json      TEXT    NOT NULL,
    PRIMARY KEY(fabric_index, group_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- matter_groupcast_groups persists the Groupcast cluster's per-group
-- properties: the multicast address policy (MulticastAddrPolicyEnum:
-- 0 IanaAddr, 1 PerGroup) and whether the group carries an auxiliary
-- access control entry. Mirrors matter.js GroupcastServer state
-- `groupProperties` (packages/node/src/behaviors/groupcast/GroupcastServer.ts).
CREATE TABLE IF NOT EXISTS matter_groupcast_groups (
    fabric_index        INTEGER NOT NULL,
    group_id            INTEGER NOT NULL CHECK(group_id BETWEEN 1 AND 65535),
    mcast_addr_policy   INTEGER NOT NULL CHECK(mcast_addr_policy BETWEEN 0 AND 255),
    has_auxiliary_acl   INTEGER NOT NULL DEFAULT 0 CHECK(has_auxiliary_acl IN (0, 1)),
    PRIMARY KEY(fabric_index, group_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- matter_server_subscriptions holds the server subscriptions of CASE
-- sessions that are active right now, so they can be re-established
-- under their old SubscriptionId after a restart (go-fabric ADR 0008,
-- mirroring matter.js SubscriptionsServer). payload is the
-- im/subscription.PeerSubscription encoding and is opaque here. The
-- bridge clears the table when it loads it at start-up and writes back
-- each subscription that becomes active again.
CREATE TABLE IF NOT EXISTS matter_server_subscriptions (
    subscription_id     INTEGER  PRIMARY KEY,
    fabric_index        INTEGER  NOT NULL,
    peer_node_id        BLOB     NOT NULL,
    payload             BLOB     NOT NULL,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS matter_server_subscriptions_fabric
    ON matter_server_subscriptions(fabric_index);

-- matter_ota_default_providers persists the OTA Software Update Requestor's
-- DefaultOTAProviders: a fabric-scoped list, nonvolatile as matter.js keeps
-- it (ota-software-update-requestor.element.ts, quality "N"). position
-- keeps the order a controller wrote. Not served by this daemon today; the
-- table keeps the schema equal to go-fabric's.
CREATE TABLE IF NOT EXISTS matter_ota_default_providers (
    fabric_index        INTEGER  NOT NULL,
    position            INTEGER  NOT NULL,
    provider_node_id    BLOB     NOT NULL,
    endpoint            INTEGER  NOT NULL CHECK(endpoint BETWEEN 0 AND 65535),
    PRIMARY KEY(fabric_index, position),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- +goose StatementEnd

-- Loss note: dropping these tables discards every fabric's group
-- memberships, Groupcast properties, the subscriptions to resume at the
-- next start and the OTA provider list; a controller re-creates its
-- groups and re-subscribes on its own. No device or config data is lost.
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS matter_ota_default_providers;
DROP INDEX IF EXISTS matter_server_subscriptions_fabric;
DROP TABLE IF EXISTS matter_server_subscriptions;
DROP TABLE IF EXISTS matter_groupcast_groups;
DROP TABLE IF EXISTS matter_group_table;
-- +goose StatementEnd
