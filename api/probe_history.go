package api

import (
	"context"
	"encoding/json"
)

// ProbeHistoryChanges runs candidate GraphQL selections that ask the public
// Manufacturing Data Model endpoint where — if anywhere — it exposes a design's
// HISTORY: the HistoryChange rows Fusion's own history panel shows (property
// edits, milestones, part-number changes) beyond the saves itemVersions lists.
//
// Why this exists. The PowerTools add-in reads that list as
// `model(modelId: <rootDataComponent.mfgdmModelId>) { history { … } }` over
// Fusion's internal mfgdm://v3 transport. This server talks to the public
// endpoint, keyed by item(hubId, itemId), and has never queried `history` or
// `model` there. APS production blocks __schema introspection but allows
// __type(name:) and __typename, so the candidates below first ask the schema
// what it has, then try the plausible roots with a tiny page. Each is its own
// independent query: an unknown field fails only that probe, never the others.
//
// The outcome gates the History tab's "Show other changes" feature (see
// docs/history/STATUS.md): the candidate that returns data is the root field
// api/history.go must use, `history_limit_*` tells it the page ceiling, and
// `author_id_space` tells it whether a change's author id and a version's
// createdBy id can be joined without a userName map. Retained as a -v-only
// diagnostic like ProbeVersionMilestones; not on any production path.
func ProbeHistoryChanges(ctx context.Context, token, hubID, itemID string) map[string]ProbeResult {
	vars := map[string]any{"hubId": hubID, "itemId": itemID}

	const historyRows = `results{__typename timestamp description author{id userName firstName lastName}}`

	candidates := map[string]string{
		// --- what the schema says it has (allowed introspection) ---
		"query_root_fields": `query{__type(name:"Query"){fields{name args{name type{name kind ofType{name}}}}}}`,
		"designItem_fields": `query{__type(name:"DesignItem"){fields{name type{name kind ofType{name kind}}}}}`,
		// null here means the public schema has no Model type at all.
		"model_type": `query{__type(name:"Model"){name fields{name type{name kind ofType{name}}}}}`,
		// The full concrete typename list; PowerTools only ever saw four of ~ten.
		"historyChange_type": `query{__type(name:"HistoryChange"){name kind fields{name} possibleTypes{name}}}`,

		// --- where the rows might hang (tiny pages, so a hit is cheap) ---
		// Preferred: the same id space as everything else in this app.
		"item_history": `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){id ... on DesignItem{history(pagination:{limit:5}){pagination{cursor} ` + historyRows + `}}}}`,
		// PowerTools' root, trying the lineage urn as the model id.
		"model_history_by_itemId": `query($itemId:ID!){model(modelId:$itemId){history(pagination:{limit:5}){pagination{cursor} ` + historyRows + `}}}`,
		// Or off the component lineage instead of the item.
		"cv_component_history": `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{tipRootComponentVersion{id component{id history(pagination:{limit:5}){pagination{cursor} results{__typename timestamp}}}}}}}`,

		// --- page ceilings; PowerTools: history rejects 100, versions accepts it ---
		"history_limit_50":   `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:50}){pagination{cursor} results{__typename}}}}}`,
		"history_limit_100":  `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:100}){pagination{cursor} results{__typename}}}}}`,
		"versions_limit_50":  `query($hubId:ID!,$itemId:ID!){itemVersions(hubId:$hubId,itemId:$itemId,pagination:{limit:50}){pagination{cursor} results{versionNumber}}}`,
		"versions_limit_100": `query($hubId:ID!,$itemId:ID!){itemVersions(hubId:$hubId,itemId:$itemId,pagination:{limit:100}){pagination{cursor} results{versionNumber}}}`,

		// --- are author.id and createdBy.id the same id space? If not, a person
		// who both saved and edited a property would get two tracks. ---
		"author_id_space": `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:5}){results{__typename author{id userName}}}}} itemVersions(hubId:$hubId,itemId:$itemId,pagination:{limit:5}){results{versionNumber createdBy{id userName}}}}`,
	}

	// The same questions against the v3 ("Collaborative Editing") endpoint,
	// where the feat/v3api branch found `history` hanging off the item. These
	// are what api/history.go relies on; the v2 set above is kept as the record
	// of why v2 could not serve it.
	v3 := map[string]string{
		"v3_item_history":       `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){__typename id ... on DesignItem{history(pagination:{limit:5}){pagination{cursor} ` + historyRows + `}}}}`,
		"v3_history_limit_50":   `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:50}){pagination{cursor} results{__typename}}}}}`,
		"v3_history_limit_100":  `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:100}){pagination{cursor} results{__typename}}}}}`,
		"v3_historyChange_type": `query{__type(name:"HistoryChange"){name kind fields{name} possibleTypes{name}}}`,
		"v3_hub_data_version":   `query($hubId:ID!){hub(hubId:$hubId){id name hubDataVersion}}`,
		// Same person, same id on both schemas? The v2 half is the version's
		// createdBy id (from the v2 probe above); compare by eye.
		"v3_author_ids": `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:5}){results{__typename author{id userName}}}}}}`,
	}
	// Releases and public shares are drawn but have no source (VersionSummary
	// .Revision / .PublicShare are reserved). PowerTools gets both from the
	// desktop DataFile — milestone NAMES ("Rev B" is a release, "Milestone V7"
	// is not) and sharedLink.isShared. These ask both schemas whether a
	// version carries a milestone name or a share flag anywhere.
	candidates["v2_designItemVersion_fields"] = `query{__type(name:"DesignItemVersion"){fields{name type{name kind ofType{name}}}}}`
	candidates["v2_componentVersion_fields"] = `query{__type(name:"ComponentVersion"){fields{name type{name kind ofType{name}}}}}`
	candidates["v2_milestone_type"] = `query{__type(name:"Milestone"){name fields{name type{name kind ofType{name}}}}}`
	v3["v3_versionCreated_fields"] = `query{__type(name:"VersionCreatedHistoryChange"){fields{name type{name kind ofType{name}}}}}`
	v3["v3_revisionCreated_fields"] = `query{__type(name:"RevisionCreatedHistoryChange"){fields{name type{name kind ofType{name}}}}}`
	v3["v3_designItem_fields"] = `query{__type(name:"DesignItem"){fields{name type{name kind ofType{name}}}}}`
	// VersionCreatedHistoryChange.version and RevisionCreatedHistoryChange
	// .revision exist (2026-09-04); their sub-fields would let a marker name
	// its version directly instead of by timestamp — worth reading once.
	v3["v3_version_type"] = `query{__type(name:"Version"){fields{name type{name kind ofType{name}}}}}`
	v3["v3_revision_type"] = `query{__type(name:"Revision"){fields{name type{name kind ofType{name}}}}}`
	v3["v3_milestone_rows"] = `query($hubId:ID!,$itemId:ID!){item(hubId:$hubId,itemId:$itemId){... on DesignItem{history(pagination:{limit:50}){results{__typename id timestamp description}}}}}`

	candidates["v2_versions_author_ids"] = `query($hubId:ID!,$itemId:ID!){itemVersions(hubId:$hubId,itemId:$itemId,pagination:{limit:5}){results{versionNumber createdBy{id userName}}}}`

	out := make(map[string]ProbeResult, len(candidates)+len(v3))
	run := func(name, q string, do func(context.Context, string, string, map[string]any) (json.RawMessage, error)) {
		r := ProbeResult{Query: q}
		data, err := do(ctx, token, q, vars)
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Data = data
		}
		out[name] = r
	}
	for name, q := range candidates {
		run(name, q, gqlQuery)
	}
	for name, q := range v3 {
		run(name, q, gqlQueryV3)
	}
	return out
}
