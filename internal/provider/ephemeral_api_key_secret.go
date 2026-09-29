package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
)

var (
	_ ephemeral.EphemeralResource              = &apiKeySecretEphemeral{}
	_ ephemeral.EphemeralResourceWithConfigure = &apiKeySecretEphemeral{}
)

// apiKeySecretEphemeral mints a secret for an API key (task RD3-11) and hands it to a consumer in the
// same run -- a vault write, a CI variable -- without it ever reaching state or a plan file. An empty
// slot is minted; a slot that already holds a secret is left alone, and its value is not returned
// (ReARM keeps only a hash), unless rotate asks for a new one.
type apiKeySecretEphemeral struct {
	client *rearm.Client
}

type apiKeySecretModel struct {
	Key         types.String `tfsdk:"key"`
	Slot        types.Int64  `tfsdk:"slot"`
	Rotate      types.Bool   `tfsdk:"rotate"`
	KeyID       types.String `tfsdk:"key_id"`
	Minted      types.Bool   `tfsdk:"minted"`
	Secret      types.String `tfsdk:"secret"`
	ExpiresDate types.String `tfsdk:"expires_date"`
}

func NewApiKeySecretEphemeral() ephemeral.EphemeralResource { return &apiKeySecretEphemeral{} }

func (e *apiKeySecretEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key_secret"
}

func (e *apiKeySecretEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Mints a secret for an API key, for a consumer in the same run; nothing is written to state or " +
			"plan files. Terraform and OpenTofu 1.10 and up. Terraform opens ephemeral resources during plan as well " +
			"as apply, so the mint happens at the first of them: an empty slot is minted once and later runs read " +
			"minted = false with no secret. rotate = true mints a new value in the slot on every open, the old one " +
			"stops working at once -- use it in a run meant to rotate, then set it back. The key must be no stronger " +
			"than the provider's key, and not one a person holds. Name a key this configuration declares by " +
			"rearm_api_key.<name>.id: the id is known only once the key exists, so the mint waits for it.",
		Attributes: map[string]schema.Attribute{
			"key":    schema.StringAttribute{Required: true, Description: "The key: its declared name, key id or uuid."},
			"slot":   schema.Int64Attribute{Optional: true, Description: "Secret slot 1 or 2; 1 when unset."},
			"rotate": schema.BoolAttribute{Optional: true, Description: "Replace the secret the slot holds; its old value stops working."},
			"key_id": schema.StringAttribute{Computed: true, Description: "The key id a client presents with the secret (REARM_APIKEYID)."},
			"minted": schema.BoolAttribute{Computed: true, Description: "Whether this open minted a value; false when the slot already held one and rotate is unset."},
			"secret": schema.StringAttribute{Computed: true, Sensitive: true,
				Description: "The minted value, this once; null when nothing was minted."},
			"expires_date": schema.StringAttribute{Computed: true, Description: "When the slot's secret expires, per the key's secret_expires_days; null for never."},
		},
	}
}

func (e *apiKeySecretEphemeral) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return
	}
	e.client = pd.client
}

// mintApiKeySecret is behind a variable so tests can see what an open sends.
var mintApiKeySecret = defaultMintApiKeySecret

func defaultMintApiKeySecret(ctx context.Context, c *rearm.Client, key string, slot int, rotate *bool) (*rearm.MintApiKeySecretResponse, error) {
	return rearm.MintApiKeySecret(ctx, c, key, slot, rotate)
}

func (e *apiKeySecretEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var m apiKeySecretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := m.mint(ctx, e.client); err != nil {
		resp.Diagnostics.AddError("ReARM could not mint the secret", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
}

// mint asks ReARM for the secret -- slot 1 unless set, rotating only when asked -- and fills the result.
func (m *apiKeySecretModel) mint(ctx context.Context, c *rearm.Client) error {
	slot := 1
	if !m.Slot.IsNull() && !m.Slot.IsUnknown() {
		slot = int(m.Slot.ValueInt64())
	}
	rotate := !m.Rotate.IsNull() && !m.Rotate.IsUnknown() && m.Rotate.ValueBool()
	out, err := mintApiKeySecret(ctx, c, m.Key.ValueString(), slot, &rotate)
	if err != nil {
		return err
	}
	if out == nil || out.MintApiKeySecretProgrammatic == nil {
		return fmt.Errorf("empty response")
	}
	minted := out.MintApiKeySecretProgrammatic
	m.Slot = types.Int64Value(int64(minted.Slot))
	m.KeyID = types.StringValue(minted.KeyId)
	m.Minted = types.BoolValue(minted.Minted)
	m.Secret = strOrNull(minted.Secret)
	m.ExpiresDate = strOrNull(minted.ExpiresDate)
	return nil
}
