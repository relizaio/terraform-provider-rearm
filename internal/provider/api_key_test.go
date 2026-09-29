package provider

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

func setOf(vs ...string) types.Set {
	elems := make([]attr.Value, 0, len(vs))
	for _, v := range vs {
		elems = append(elems, types.StringValue(v))
	}
	s, _ := types.SetValue(types.StringType, elems)
	return s
}

func freeformKey() *apiKeyModel {
	return &apiKeyModel{Name: types.StringValue("ci-release"), Type: types.StringValue("FREEFORM"),
		Object: types.StringNull(), Notes: types.StringNull(), Status: types.StringNull(),
		SecretExpiresDays: types.Int64Value(90), SessionMaxMinutes: types.Int64Null(),
		Permissions: &apiKeyPermissions{Type: types.StringValue("READ_WRITE"), Functions: setOf("BOARD_WRITE", "CONFIGURATION_READ"),
			Approvals: types.SetNull(types.StringType)}}
}

// rearm_api_key (task RD3-11) sends one key as a non-authoritative API_KEYS file: what the configuration
// sets, a new key ACTIVE when status is unset, the permission sets as lists; never anything secret.
func TestApiKeySendsItsSettingsAndANewKeyActive(t *testing.T) {
	var sent []map[string]any
	applySpec = func(_ context.Context, _ *rearm.Client, f any, _ bool, _ *catalog.Source) (*catalog.Result, error) {
		spec := f.(*catalog.ApiKeysFile).Spec
		if spec["kind"] != "API_KEYS" || spec["authoritative"] != false {
			t.Errorf("a one-key, non-authoritative file: %v", spec)
		}
		sent = append(sent, spec["keys"].([]any)[0].(map[string]any))
		return &catalog.Result{}, nil
	}
	defer func() { applySpec = catalog.Apply }()
	r := &apiKeyResource{}
	m := freeformKey()
	r.apply(context.Background(), m, true, &diagAdder{})
	r.apply(context.Background(), m, false, &diagAdder{})
	created, updated := sent[0], sent[1]
	if created["status"] != "ACTIVE" {
		t.Errorf("a create brings a deactivated key back: %v", created)
	}
	if _, has := updated["status"]; has {
		t.Errorf("an update leaves an unmanaged status alone: %v", updated)
	}
	for _, k := range []string{"notes", "object", "sessionMaxMinutes"} {
		if _, has := created[k]; has {
			t.Errorf("unset %s is not sent: %v", k, created)
		}
	}
	perms := created["permissions"].(map[string]any)
	fs := []string{}
	for _, f := range perms["functions"].([]any) {
		fs = append(fs, f.(string))
	}
	sort.Strings(fs)
	if perms["type"] != "READ_WRITE" || !reflect.DeepEqual(fs, []string{"BOARD_WRITE", "CONFIGURATION_READ"}) {
		t.Errorf("permissions: %v", perms)
	}
	if created["secretExpiresDays"] != int64(90) {
		t.Errorf("secretExpiresDays: %v", created)
	}
}

func view() map[string]any {
	return map[string]any{"uuid": "u-1", "keyId": "FREEFORM__org__ord__o1", "name": "ci-release", "type": "FREEFORM",
		"status": "ACTIVE", "notes": nil, "secretExpiresDays": float64(90), "sessionMaxMinutes": nil,
		"permissions": map[string]any{"type": "READ_WRITE", "functions": []any{"BOARD_WRITE", "CONFIGURATION_READ"},
			"objects": []any{
				map[string]any{"scope": "COMPONENT", "object": "b-uuid", "type": "READ_ONLY"},
				map[string]any{"scope": "BOARD", "object": "a-uuid", "type": "READ_WRITE", "functions": []any{"BOARD_WRITE"}},
			}},
		"secretSlots": []any{map[string]any{"slot": float64(2), "active": true, "createdDate": "2026-09-29T00:00:00Z"}}}
}

// Read back: the computed id and the slots' metadata; configured spellings ReARM holds as equivalents kept.
func TestApiKeyReadsBackKeepingEquivalentSpellings(t *testing.T) {
	m := freeformKey()
	m.Notes = types.StringValue("  ")
	m.Permissions.Objects = []apiKeyObjectPermission{
		{Scope: types.StringValue("BOARD"), Object: types.StringValue("A-UUID"), Type: types.StringValue("READ_WRITE"),
			Functions: setOf("BOARD_WRITE"), Approvals: setOf()},
		{Scope: types.StringValue("COMPONENT"), Object: types.StringValue("b-uuid"), Type: types.StringValue("READ_ONLY"),
			Functions: types.SetNull(types.StringType), Approvals: types.SetNull(types.StringType)},
	}
	m.fromView(view(), false)
	if m.ID.ValueString() != "FREEFORM__org__ord__o1" || m.UUID.ValueString() != "u-1" {
		t.Errorf("id and uuid: %v %v", m.ID, m.UUID)
	}
	if m.Notes.ValueString() != "  " {
		t.Errorf("a blank note ReARM stores as none keeps the configured blank: %q", m.Notes)
	}
	if len(m.Permissions.Objects) != 2 || m.Permissions.Objects[0].Object.ValueString() != "A-UUID" ||
		m.Permissions.Objects[1].Scope.ValueString() != "COMPONENT" {
		t.Errorf("grants follow the configured order and spelling: %+v", m.Permissions.Objects)
	}
	if !m.Permissions.Objects[0].Approvals.Equal(setOf()) {
		t.Errorf("a configured empty set stays: %v", m.Permissions.Objects[0].Approvals)
	}
	if !m.Status.IsNull() || !m.SessionMaxMinutes.IsNull() {
		t.Errorf("an unmanaged attribute stays unmanaged: %v %v", m.Status, m.SessionMaxMinutes)
	}
	if len(m.SecretSlots) != 1 || m.SecretSlots[0].Slot.ValueInt64() != 2 || !m.SecretSlots[0].ExpiresDate.IsNull() {
		t.Errorf("slots: %+v", m.SecretSlots)
	}

	// Import reads everything; a grant set outside the configuration is drift.
	imported := &apiKeyModel{Name: types.StringValue("ci-release")}
	imported.fromView(view(), true)
	if imported.Status.ValueString() != "ACTIVE" || imported.Permissions == nil || len(imported.Permissions.Objects) != 2 {
		t.Errorf("import: %+v", imported)
	}
	extra := freeformKey()
	extra.Permissions.Objects = []apiKeyObjectPermission{}
	extra.fromView(view(), false)
	if len(extra.Permissions.Objects) != 2 {
		t.Errorf("grants ReARM has and the configuration not are read, so the plan shows them: %+v", extra.Permissions.Objects)
	}
}

// A COMPONENT key's component, named or by uuid, is the one ReARM holds.
func TestApiKeyKeepsTheConfiguredComponentSpelling(t *testing.T) {
	for _, configured := range []string{"billing", "C-UUID"} {
		got := sameObject(types.StringValue(configured), "billing", "c-uuid")
		if got.ValueString() != configured {
			t.Errorf("%s read back as %v", configured, got)
		}
	}
	if got := sameObject(types.StringValue("other"), "billing", "c-uuid"); got.ValueString() != "billing" {
		t.Errorf("another component is drift: %v", got)
	}
}

// A key deactivated outside Terraform -- as a destroy leaves it -- reads as gone, so the plan creates it
// again; an import still finds it.
func TestAnInactiveKeyReadsAsGoneExceptOnImport(t *testing.T) {
	readApiKey = func(context.Context, *rearm.Client, string) (map[string]any, error) {
		v := view()
		v["status"] = "INACTIVE"
		return v, nil
	}
	defer func() { readApiKey = defaultReadApiKey }()
	r := &apiKeyResource{}
	if r.read(context.Background(), freeformKey(), false, &diagAdder{}) {
		t.Error("an inactive key with status unmanaged is gone")
	}
	if !r.read(context.Background(), &apiKeyModel{Name: types.StringValue("ci-release")}, true, &diagAdder{}) {
		t.Error("an import finds an inactive key")
	}
}

// The ephemeral secret asks for slot 1 unless told, rotates only when told, and carries the value only when
// one was minted.
func TestTheEphemeralSecretMintsOnceAndRotatesOnlyWhenAsked(t *testing.T) {
	var calls []string
	mintApiKeySecret = func(_ context.Context, _ *rearm.Client, key string, slot int, rotate *bool) (*rearm.MintApiKeySecretResponse, error) {
		calls = append(calls, key)
		secret := "s3cret"
		out := &rearm.MintApiKeySecretMintApiKeySecretProgrammaticMintedApiKeySecret{KeyId: "FREEFORM__o__ord__1", Slot: slot, Minted: *rotate || slot == 1}
		if out.Minted {
			out.Secret = &secret
		}
		return &rearm.MintApiKeySecretResponse{MintApiKeySecretProgrammatic: out}, nil
	}
	defer func() { mintApiKeySecret = defaultMintApiKeySecret }()
	m := &apiKeySecretModel{Key: types.StringValue("ci-release"), Slot: types.Int64Null(), Rotate: types.BoolNull()}
	if err := m.mint(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if m.Slot.ValueInt64() != 1 || !m.Minted.ValueBool() || m.Secret.ValueString() != "s3cret" || m.KeyID.ValueString() != "FREEFORM__o__ord__1" {
		t.Errorf("slot 1 minted: %+v", m)
	}
	held := &apiKeySecretModel{Key: types.StringValue("ci-release"), Slot: types.Int64Value(2), Rotate: types.BoolNull()}
	_ = held.mint(context.Background(), nil)
	if held.Minted.ValueBool() || !held.Secret.IsNull() {
		t.Errorf("a held slot returns no value: %+v", held)
	}
	rotated := &apiKeySecretModel{Key: types.StringValue("ci-release"), Slot: types.Int64Value(2), Rotate: types.BoolValue(true)}
	_ = rotated.mint(context.Background(), nil)
	if !rotated.Minted.ValueBool() || rotated.Secret.ValueString() != "s3cret" {
		t.Errorf("rotate mints: %+v", rotated)
	}

	// Its schema: the value is sensitive, and the provider serves it.
	e := NewApiKeySecretEphemeral()
	var resp ephemeral.SchemaResponse
	e.Schema(context.Background(), ephemeral.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("ephemeral schema: %v", diags)
	}
	if !resp.Schema.Attributes["secret"].IsSensitive() {
		t.Error("the secret is sensitive")
	}
	p := New("test")().(provider.ProviderWithEphemeralResources)
	var names []string
	for _, f := range p.EphemeralResources(context.Background()) {
		var meta ephemeral.MetadataResponse
		f().Metadata(context.Background(), ephemeral.MetadataRequest{ProviderTypeName: "rearm"}, &meta)
		names = append(names, meta.TypeName)
	}
	if !reflect.DeepEqual(names, []string{"rearm_api_key_secret"}) {
		t.Errorf("ephemeral resources: %v", names)
	}
}

// Destroy deactivates the key, and one already gone is no error.
func TestApiKeyDeleteArchives(t *testing.T) {
	var archived []string
	archiveApiKey = func(_ context.Context, _ *rearm.Client, name string) (*rearm.ArchiveApiKeyResponse, error) {
		archived = append(archived, name)
		return nil, nil
	}
	defer func() { archiveApiKey = defaultArchiveApiKey }()
	if _, err := archiveApiKey(context.Background(), nil, "ci-release"); err != nil || !reflect.DeepEqual(archived, []string{"ci-release"}) {
		t.Errorf("archive: %v %v", archived, err)
	}
}
