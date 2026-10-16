import {
  ActionIcon,
  Autocomplete,
  Button,
  Group,
  NumberInput,
  PasswordInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from '@mantine/core';
import { IconRefresh } from '@tabler/icons-react';
import { useMutation } from '@tanstack/react-query';
import { useState } from 'react';

import { type LLMProvider, type LLMProviderInput, listLLMModels } from '../api/client';
import { notifyError } from '../lib/notify';

type Mode = LLMProvider['structuredOutput'];

/**
 * Known OpenAI-compatible endpoints. Only stable facts live here (base URL,
 * where the daily quota resets); models and limits change too often and
 * are entered by the user ("Load models" asks the server).
 */
const presets: Record<string, { label: string; baseUrl: string; quotaTimezone?: string }> = {
  gemini: {
    label: 'Google Gemini',
    baseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai/',
    quotaTimezone: 'America/Los_Angeles',
  },
  groq: { label: 'Groq', baseUrl: 'https://api.groq.com/openai/v1/' },
  openrouter: { label: 'OpenRouter', baseUrl: 'https://openrouter.ai/api/v1/' },
  cerebras: { label: 'Cerebras', baseUrl: 'https://api.cerebras.ai/v1/' },
  mistral: { label: 'Mistral', baseUrl: 'https://api.mistral.ai/v1/' },
  ollama: { label: 'Ollama (local)', baseUrl: 'http://ollama:11434/v1/' },
};

const modes: { value: Mode; label: string }[] = [
  { value: 'json_schema', label: 'JSON schema (response_format)' },
  { value: 'json_object', label: 'JSON object + schema in prompt' },
  { value: 'prompt', label: 'Schema in prompt only' },
];

const timeZones = (() => {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return ['UTC'];
  }
})();

type Values = {
  name: string;
  baseUrl: string;
  model: string;
  apiKey: string;
  enabled: boolean;
  structuredOutput: Mode;
  rpm: number | string;
  tpm: number | string;
  rpd: number | string;
  tpd: number | string;
  maxOutputTokens: number | string;
  timeoutSeconds: number | string;
  quotaTimezone: string;
  extraParams: string;
};

function initial(p?: LLMProvider): Values {
  return {
    name: p?.name ?? '',
    baseUrl: p?.baseUrl ?? '',
    model: p?.model ?? '',
    apiKey: '',
    enabled: p?.enabled ?? true,
    structuredOutput: p?.structuredOutput ?? 'json_schema',
    rpm: p?.limits.rpm ?? 0,
    tpm: p?.limits.tpm ?? 0,
    rpd: p?.limits.rpd ?? 0,
    tpd: p?.limits.tpd ?? 0,
    maxOutputTokens: p?.maxOutputTokens ?? 0,
    timeoutSeconds: p?.timeoutSeconds ?? 300,
    quotaTimezone: p?.quotaTimezone ?? 'UTC',
    extraParams: p?.extraParams ?? '',
  };
}

const num = (v: number | string) => (typeof v === 'number' ? v : Number(v) || 0);

type Props = {
  /** The provider to edit; a new one is created without it. */
  provider?: LLMProvider;
  submitting: boolean;
  /** The body fits both creating and updating. */
  onSubmit: (body: LLMProviderInput) => void;
};

/** Create/edit form of a UI-managed LLM provider. */
export function LLMProviderForm({ provider, submitting, onSubmit }: Props) {
  const [v, setV] = useState<Values>(() => initial(provider));
  const [removeKey, setRemoveKey] = useState(false);
  const set =
    <K extends keyof Values>(k: K) =>
    (value: Values[K]) =>
      setV((old) => ({ ...old, [k]: value }));

  const models = useMutation({
    mutationFn: () =>
      listLLMModels({
        providerId: provider?.id,
        baseUrl: v.baseUrl || undefined,
        apiKey: v.apiKey || undefined,
      }),
    onError: (e) => notifyError('Could not load models', e),
  });

  const applyPreset = (key: string | null) => {
    const p = key ? presets[key] : undefined;
    if (!p) {
      return;
    }
    setV((old) => ({
      ...old,
      name: old.name || key!,
      baseUrl: p.baseUrl,
      quotaTimezone: p.quotaTimezone ?? old.quotaTimezone,
    }));
    models.reset();
  };

  const extraValid = (() => {
    if (!v.extraParams.trim()) {
      return true;
    }
    try {
      const parsed: unknown = JSON.parse(v.extraParams);
      return typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed);
    } catch {
      return false;
    }
  })();

  const submit = () => {
    const body: LLMProviderInput = {
      name: v.name.trim(),
      baseUrl: v.baseUrl.trim(),
      model: v.model.trim(),
      enabled: v.enabled,
      structuredOutput: v.structuredOutput,
      limits: { rpm: num(v.rpm), tpm: num(v.tpm), rpd: num(v.rpd), tpd: num(v.tpd) },
      maxOutputTokens: num(v.maxOutputTokens),
      timeoutSeconds: num(v.timeoutSeconds) || 300,
      quotaTimezone: v.quotaTimezone,
      extraParams: v.extraParams.trim(),
    };
    if (v.apiKey) {
      body.apiKey = v.apiKey;
    } else if (removeKey) {
      body.apiKey = '';
    }
    onSubmit(body);
  };

  // The server only sends a stored key to the URL it was stored for.
  const keyNeeded =
    !!provider?.hasApiKey && v.baseUrl.trim() !== provider.baseUrl && !v.apiKey && !removeKey;
  const canSubmit = v.name.trim() && v.baseUrl.trim() && v.model.trim() && extraValid && !keyNeeded;

  return (
    <Stack gap="sm">
      {!provider && (
        <Select
          label="Preset"
          placeholder="Fill in a known provider (optional)"
          data={Object.entries(presets).map(([value, p]) => ({ value, label: p.label }))}
          onChange={applyPreset}
          clearable
        />
      )}
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <TextInput
          label="Name"
          required
          value={v.name}
          onChange={(e) => set('name')(e.currentTarget.value)}
        />
        <TextInput
          label="Base URL"
          required
          placeholder="https://…/v1/"
          value={v.baseUrl}
          onChange={(e) => set('baseUrl')(e.currentTarget.value)}
        />
      </SimpleGrid>
      <PasswordInput
        label="API key"
        placeholder={provider?.hasApiKey ? 'unchanged' : 'none (e.g. a local server)'}
        description="Stored in the database; never shown again."
        value={v.apiKey}
        error={keyNeeded ? 'Enter the key again for the new base URL (or remove it)' : undefined}
        onChange={(e) => set('apiKey')(e.currentTarget.value)}
      />
      {provider?.hasApiKey && !v.apiKey && (
        <Switch
          label="Remove the stored key"
          checked={removeKey}
          onChange={(e) => setRemoveKey(e.currentTarget.checked)}
        />
      )}
      <Group align="flex-end" gap="xs" wrap="nowrap">
        <Autocomplete
          label="Model"
          required
          style={{ flex: 1 }}
          value={v.model}
          onChange={set('model')}
          data={models.data ?? []}
          limit={50}
          placeholder={models.data ? `${models.data.length} models available` : 'model ID'}
        />
        <Tooltip label="Load models from the server">
          <ActionIcon
            size="lg"
            variant="default"
            aria-label="Load models"
            loading={models.isPending}
            disabled={!v.baseUrl.trim() && !provider}
            onClick={() => models.mutate()}
          >
            <IconRefresh size={16} />
          </ActionIcon>
        </Tooltip>
      </Group>
      <Select
        label="Structured output"
        description="Use a weaker mode when the provider rejects JSON schemas; answers are validated either way."
        data={modes}
        value={v.structuredOutput}
        onChange={(m) => m && set('structuredOutput')(m as Mode)}
        allowDeselect={false}
      />
      <div>
        <Text size="sm" fw={500}>
          Limits
        </Text>
        <Text size="xs" c="dimmed" mb={4}>
          From the provider&apos;s documentation for your plan; 0 means no limit. Daily limits reset
          at midnight in the quota time zone.
        </Text>
        <SimpleGrid cols={{ base: 2, sm: 4 }}>
          <NumberInput label="Requests / min" min={0} value={v.rpm} onChange={set('rpm')} />
          <NumberInput
            label="Tokens / min"
            min={0}
            thousandSeparator=","
            value={v.tpm}
            onChange={set('tpm')}
          />
          <NumberInput label="Requests / day" min={0} value={v.rpd} onChange={set('rpd')} />
          <NumberInput
            label="Tokens / day"
            min={0}
            thousandSeparator=","
            value={v.tpd}
            onChange={set('tpd')}
          />
        </SimpleGrid>
      </div>
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        <Autocomplete
          label="Quota time zone"
          value={v.quotaTimezone}
          onChange={set('quotaTimezone')}
          data={timeZones}
          limit={20}
        />
        <NumberInput
          label="Max output tokens"
          description="0: server default"
          min={0}
          value={v.maxOutputTokens}
          onChange={set('maxOutputTokens')}
        />
        <NumberInput
          label="Timeout (s)"
          description="per request"
          min={1}
          max={3600}
          value={v.timeoutSeconds}
          onChange={set('timeoutSeconds')}
        />
      </SimpleGrid>
      <Textarea
        label="Extra request parameters"
        description='JSON object merged into every request, e.g. {"reasoning_effort": "low"}'
        autosize
        minRows={2}
        value={v.extraParams}
        error={extraValid ? undefined : 'must be a JSON object'}
        onChange={(e) => set('extraParams')(e.currentTarget.value)}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
      />
      <Switch
        label="Enabled"
        checked={v.enabled}
        onChange={(e) => set('enabled')(e.currentTarget.checked)}
      />
      <Group justify="flex-end">
        <Button onClick={submit} loading={submitting} disabled={!canSubmit}>
          {provider ? 'Save' : 'Add provider'}
        </Button>
      </Group>
    </Stack>
  );
}
