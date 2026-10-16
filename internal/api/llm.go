package api

import (
	"context"
	"errors"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/store"
)

func toLLMProvider(st llm.Status) LLMProvider {
	p := st.Provider
	day, _ := time.Parse(time.DateOnly, st.Today.Day)
	out := LLMProvider{
		Id:               p.ID,
		Name:             p.Name,
		Source:           LLMProviderSource(p.Source),
		Position:         p.Position,
		Enabled:          p.Enabled,
		BaseUrl:          p.BaseURL,
		Model:            p.Model,
		HasApiKey:        p.APIKey != "" || p.APIKeyEnv != "",
		ApiKeyEnv:        nonEmpty(p.APIKeyEnv),
		StructuredOutput: LLMStructuredOutput(p.StructuredOutput),
		ExtraParams:      nonEmpty(p.ExtraParams),
		Limits:           LLMLimits{Rpm: &p.RPM, Tpm: &p.TPM, Rpd: &p.RPD, Tpd: &p.TPD},
		MaxOutputTokens:  p.MaxOutputTokens,
		TimeoutSeconds:   p.TimeoutSeconds,
		QuotaTimezone:    p.QuotaTimezone,
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
		Status: LLMProviderStatus{
			State:  LLMProviderState(st.State),
			Reason: nonEmpty(st.Reason),
			Today: LLMUsageDay{
				Day:          openapi_types.Date{Time: day},
				Requests:     st.Today.Requests,
				InputTokens:  st.Today.InputTokens,
				OutputTokens: st.Today.OutputTokens,
				Errors:       st.Today.Errors,
				RateLimited:  st.Today.RateLimited,
			},
			MinuteRequests: st.MinuteRequests,
			MinuteTokens:   st.MinuteTokens,
			AvailableAt:    st.AvailableAt,
			QuotaResetAt:   st.QuotaResetAt,
			LastError:      nonEmpty(st.LastError),
			LastErrorAt:    st.LastErrorAt,
		},
	}
	return out
}

// providerStatus returns the API view of one provider.
func (s *Server) providerStatus(ctx context.Context, id int64) (LLMProvider, error) {
	all, err := s.LLM.Status(ctx)
	if err != nil {
		return LLMProvider{}, err
	}
	for _, st := range all {
		if st.Provider.ID == id {
			return toLLMProvider(st), nil
		}
	}
	return LLMProvider{}, store.ErrNotFound
}

// ListLLMProviders returns all providers with their quota usage.
func (s *Server) ListLLMProviders(ctx context.Context, _ ListLLMProvidersRequestObject) (ListLLMProvidersResponseObject, error) {
	all, err := s.LLM.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LLMProvider, 0, len(all))
	for _, st := range all {
		out = append(out, toLLMProvider(st))
	}
	return ListLLMProviders200JSONResponse(out), nil
}

func applyLimits(p *store.LLMProvider, l *LLMLimits) {
	if l == nil {
		return
	}
	setIf(&p.RPM, l.Rpm)
	setIf(&p.TPM, l.Tpm)
	setIf(&p.RPD, l.Rpd)
	setIf(&p.TPD, l.Tpd)
}

func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// CreateLLMProvider adds a provider managed in the UI.
func (s *Server) CreateLLMProvider(ctx context.Context, req CreateLLMProviderRequestObject) (CreateLLMProviderResponseObject, error) {
	b := req.Body
	p := &store.LLMProvider{
		Name:    b.Name,
		Source:  store.ProviderSourceUI,
		Enabled: true,
		BaseURL: b.BaseUrl,
		Model:   b.Model,
	}
	setIf(&p.APIKey, b.ApiKey)
	setIf(&p.Enabled, b.Enabled)
	setIf(&p.ExtraParams, b.ExtraParams)
	setIf(&p.MaxOutputTokens, b.MaxOutputTokens)
	setIf(&p.TimeoutSeconds, b.TimeoutSeconds)
	setIf(&p.QuotaTimezone, b.QuotaTimezone)
	if b.StructuredOutput != nil {
		p.StructuredOutput = string(*b.StructuredOutput)
	}
	applyLimits(p, b.Limits)
	if b.Position != nil {
		p.Position = *b.Position
	} else {
		all, err := s.Store.ListLLMProviders(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range all {
			p.Position = max(p.Position, o.Position+1)
		}
	}
	llm.Normalize(p)
	if err := llm.Validate(p); err != nil {
		return CreateLLMProvider400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	}
	if err := s.Store.CreateLLMProvider(ctx, p); errors.Is(err, store.ErrConflict) {
		return CreateLLMProvider409JSONResponse{Message: "a provider with this name already exists"}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.providerStatus(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return CreateLLMProvider201JSONResponse(out), nil
}

const configProviderMsg = "this provider is defined in the Helm values (llm.providers); change it there"

// UpdateLLMProvider changes a UI provider.
func (s *Server) UpdateLLMProvider(ctx context.Context, req UpdateLLMProviderRequestObject) (UpdateLLMProviderResponseObject, error) {
	p, err := s.Store.GetLLMProvider(ctx, req.ProviderId)
	if isNotFound(err) {
		return UpdateLLMProvider404JSONResponse{Message: "provider not found"}, nil
	} else if err != nil {
		return nil, err
	}
	if p.Source == store.ProviderSourceConfig {
		return UpdateLLMProvider409JSONResponse{Message: configProviderMsg}, nil
	}
	b := req.Body
	if b.BaseUrl != nil && *b.BaseUrl != p.BaseURL && p.APIKey != "" && b.ApiKey == nil {
		// Otherwise the stored key could be sent to any server.
		return UpdateLLMProvider400JSONResponse{ErrorJSONResponse(errMsg(
			"enter the API key again (or an empty one to remove it) when changing the base URL"))}, nil
	}
	setIf(&p.Name, b.Name)
	setIf(&p.BaseURL, b.BaseUrl)
	setIf(&p.Model, b.Model)
	setIf(&p.APIKey, b.ApiKey)
	setIf(&p.Enabled, b.Enabled)
	setIf(&p.Position, b.Position)
	setIf(&p.ExtraParams, b.ExtraParams)
	setIf(&p.MaxOutputTokens, b.MaxOutputTokens)
	setIf(&p.TimeoutSeconds, b.TimeoutSeconds)
	setIf(&p.QuotaTimezone, b.QuotaTimezone)
	if b.StructuredOutput != nil {
		p.StructuredOutput = string(*b.StructuredOutput)
	}
	applyLimits(p, b.Limits)
	llm.Normalize(p)
	if err := llm.Validate(p); err != nil {
		return UpdateLLMProvider400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	}
	if err := s.Store.UpdateLLMProvider(ctx, p); errors.Is(err, store.ErrConflict) {
		return UpdateLLMProvider409JSONResponse{Message: "a provider with this name already exists"}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.providerStatus(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return UpdateLLMProvider200JSONResponse(out), nil
}

// DeleteLLMProvider removes a UI provider.
func (s *Server) DeleteLLMProvider(ctx context.Context, req DeleteLLMProviderRequestObject) (DeleteLLMProviderResponseObject, error) {
	p, err := s.Store.GetLLMProvider(ctx, req.ProviderId)
	if isNotFound(err) {
		return DeleteLLMProvider404JSONResponse{ErrorJSONResponse(errMsg("provider not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if p.Source == store.ProviderSourceConfig {
		return DeleteLLMProvider409JSONResponse{Message: configProviderMsg}, nil
	}
	if err := s.Store.DeleteLLMProvider(ctx, p.ID); err != nil {
		return nil, err
	}
	s.LLM.Forget(p.ID)
	return DeleteLLMProvider204Response{}, nil
}

// TestLLMProvider sends the provider test prompt.
func (s *Server) TestLLMProvider(ctx context.Context, req TestLLMProviderRequestObject) (TestLLMProviderResponseObject, error) {
	p, err := s.Store.GetLLMProvider(ctx, req.ProviderId)
	if isNotFound(err) {
		return TestLLMProvider404JSONResponse{ErrorJSONResponse(errMsg("provider not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	res, err := s.LLM.Test(ctx, p)
	if err != nil {
		return nil, err
	}
	out := LLMTestResult{Ok: res.OK, Message: res.Message, Model: nonEmpty(res.Model)}
	if res.Latency > 0 {
		ms := res.Latency.Milliseconds()
		out.LatencyMs = &ms
		out.InputTokens, out.OutputTokens = &res.Usage.InputTokens, &res.Usage.OutputTokens
	}
	return TestLLMProvider200JSONResponse(out), nil
}

// ListLLMModels asks a server for its models.
func (s *Server) ListLLMModels(ctx context.Context, req ListLLMModelsRequestObject) (ListLLMModelsResponseObject, error) {
	b := req.Body
	p := &store.LLMProvider{Model: "-"}
	if b.ProviderId != nil {
		stored, err := s.Store.GetLLMProvider(ctx, *b.ProviderId)
		if isNotFound(err) {
			return ListLLMModels404JSONResponse{Message: "provider not found"}, nil
		} else if err != nil {
			return nil, err
		}
		p = stored
	}
	if b.BaseUrl != nil && *b.BaseUrl != "" && *b.BaseUrl != p.BaseURL {
		// A stored key is only ever sent to the URL it was stored for.
		p.BaseURL, p.APIKey, p.APIKeyEnv = *b.BaseUrl, "", ""
	}
	if b.ApiKey != nil && *b.ApiKey != "" {
		p.APIKey, p.APIKeyEnv = *b.ApiKey, ""
	}
	p.ExtraParams = ""
	llm.Normalize(p)
	if p.BaseURL == "" {
		return ListLLMModels400JSONResponse{ErrorJSONResponse(errMsg("baseUrl or providerId is required"))}, nil
	}
	if p.Name == "" {
		p.Name = "new"
	}
	if err := llm.Validate(p); err != nil {
		return ListLLMModels400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	}
	models, err := s.LLM.Models(ctx, p)
	if err != nil {
		return ListLLMModels502JSONResponse{Message: "list models: " + err.Error()}, nil
	}
	return ListLLMModels200JSONResponse(models), nil
}
