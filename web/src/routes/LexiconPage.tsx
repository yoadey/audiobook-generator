import {
  ActionIcon,
  Alert,
  Badge,
  Breadcrumbs,
  Button,
  Center,
  Checkbox,
  Group,
  Loader,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core';
import { IconPencil, IconSearch, IconTrash } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { useState } from 'react';

import {
  bookQuery,
  createLexiconEntry,
  deleteLexiconEntry,
  type LexiconEntryInput,
  lexiconQuery,
  updateLexiconEntry,
} from '../api/client';
import { AnchorLink } from '../components/links';
import { VoicePreviewButton } from '../components/VoicePreviewButton';
import { notifyError, notifySuccess } from '../lib/notify';
import { usePreview } from '../lib/preview';

/** The text Kokoro is given for a draft entry (the server does the same for saved ones). */
const spoken = (e: LexiconEntryInput) =>
  e.phonemes ? `[${e.term.trim()}](/${e.replacement.trim()}/)` : e.replacement.trim();

function EntryForm({
  initial,
  voice,
  speed,
  preview,
  submitLabel,
  pending,
  onSubmit,
  onCancel,
}: {
  initial: LexiconEntryInput;
  voice: string;
  speed: number;
  preview: ReturnType<typeof usePreview>;
  submitLabel: string;
  pending: boolean;
  onSubmit: (e: LexiconEntryInput) => void;
  onCancel?: () => void;
}) {
  const [term, setTerm] = useState(initial.term);
  const [replacement, setReplacement] = useState(initial.replacement);
  const [phonemes, setPhonemes] = useState(initial.phonemes ?? false);
  const draft = { term, replacement, phonemes };
  const valid = term.trim() !== '' && replacement.trim() !== '';
  return (
    <form
      onSubmit={(ev) => {
        ev.preventDefault();
        if (valid) {
          onSubmit({ term: term.trim(), replacement: replacement.trim(), phonemes });
        }
      }}
    >
      <Group align="flex-end" wrap="wrap">
        <TextInput
          label="Word or name"
          placeholder="Ryoka"
          maxLength={100}
          value={term}
          onChange={(e) => setTerm(e.currentTarget.value)}
        />
        <TextInput
          label={phonemes ? 'Phonemes (IPA)' : 'Say instead'}
          placeholder={phonemes ? 'ɹiˈoʊkə' : 'Ree-oh-ka'}
          maxLength={200}
          ff={phonemes ? 'monospace' : undefined}
          value={replacement}
          onChange={(e) => setReplacement(e.currentTarget.value)}
        />
        <Checkbox
          mb={8}
          label="Phonemes"
          checked={phonemes}
          onChange={(e) => setPhonemes(e.currentTarget.checked)}
        />
        <VoicePreviewButton
          id="draft"
          voice={voice}
          speed={speed}
          text={valid ? spoken(draft) : undefined}
          preview={preview}
          label="Listen to this pronunciation"
        />
        <Button type="submit" disabled={!valid} loading={pending}>
          {submitLabel}
        </Button>
        {onCancel && (
          <Button variant="default" onClick={onCancel}>
            Cancel
          </Button>
        )}
      </Group>
    </form>
  );
}

export function LexiconPage() {
  const { bookId: raw } = useParams({ from: '/books/$bookId/lexicon' });
  const bookId = Number(raw);
  const queryClient = useQueryClient();
  const book = useQuery(bookQuery(bookId));
  const entries = useQuery(lexiconQuery(bookId));
  const preview = usePreview(bookId);
  const [search, setSearch] = useState('');
  const [adding, setAdding] = useState(0); // remounts the add form after a save
  const [editing, setEditing] = useState<number | null>(null);

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['books', bookId] });

  const create = useMutation({
    mutationFn: (e: LexiconEntryInput) => createLexiconEntry(bookId, e),
    onSuccess: (e) => {
      notifySuccess(`Added “${e.term}”`);
      setAdding((n) => n + 1);
      return invalidate();
    },
    onError: (e) => notifyError('Could not add the pronunciation', e),
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: number; body: LexiconEntryInput }) =>
      updateLexiconEntry(id, body),
    onSuccess: () => {
      setEditing(null);
      return invalidate();
    },
    onError: (e) => notifyError('Could not save the pronunciation', e),
  });
  const remove = useMutation({
    mutationFn: (id: number) => deleteLexiconEntry(id),
    onSuccess: invalidate,
    onError: (e) => notifyError('Could not delete the pronunciation', e),
  });

  const voice = book.data?.narratorVoice ?? '';
  const speed = book.data?.narratorSpeed ?? 1;

  const q = search.trim().toLowerCase();
  const shown = (entries.data ?? []).filter(
    (e) => !q || e.term.toLowerCase().includes(q) || e.replacement.toLowerCase().includes(q),
  );

  return (
    <Stack>
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <AnchorLink to="/books/$bookId" params={{ bookId: String(bookId) }} size="sm">
          {book.data?.title ?? 'Book'}
        </AnchorLink>
        <Text size="sm">Pronunciation</Text>
      </Breadcrumbs>
      <Title order={2}>Pronunciation</Title>
      <Text size="sm" c="dimmed">
        Words and names that are spoken differently from how they are written. Entries match whole
        words in any case; a change marks the rendered chapters containing the word as outdated.
        Phonemes use Kokoro’s notation (IPA, e.g. ɹiˈoʊkə).
      </Text>

      <EntryForm
        key={adding}
        initial={{ term: '', replacement: '', phonemes: false }}
        voice={voice}
        speed={speed}
        preview={preview}
        submitLabel="Add"
        pending={create.isPending}
        onSubmit={(e) => create.mutate(e)}
      />

      {entries.isPending ? (
        <Center>
          <Loader />
        </Center>
      ) : entries.isError ? (
        <Alert color="red" title="Could not load the pronunciations">
          {entries.error.message}
        </Alert>
      ) : entries.data.length === 0 ? (
        <Text c="dimmed">No pronunciations yet.</Text>
      ) : (
        <>
          <TextInput
            placeholder="Search"
            leftSection={<IconSearch size={16} />}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            maw={300}
          />
          <Table striped>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Word or name</Table.Th>
                <Table.Th>Spoken as</Table.Th>
                <Table.Th w={130} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {shown.map((e) =>
                editing === e.id ? (
                  <Table.Tr key={e.id}>
                    <Table.Td colSpan={3}>
                      <EntryForm
                        initial={e}
                        voice={voice}
                        speed={speed}
                        preview={preview}
                        submitLabel="Save"
                        pending={update.isPending}
                        onSubmit={(body) => update.mutate({ id: e.id, body })}
                        onCancel={() => setEditing(null)}
                      />
                    </Table.Td>
                  </Table.Tr>
                ) : (
                  <Table.Tr key={e.id}>
                    <Table.Td>{e.term}</Table.Td>
                    <Table.Td>
                      <Group gap="xs">
                        <Text size="sm" ff={e.phonemes ? 'monospace' : undefined}>
                          {e.replacement}
                        </Text>
                        {e.phonemes && (
                          <Badge size="xs" variant="light">
                            IPA
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        <VoicePreviewButton
                          id={`entry-${e.id}`}
                          voice={voice}
                          speed={speed}
                          text={e.term}
                          preview={preview}
                          label={`Listen to ${e.term}`}
                        />
                        <Tooltip label="Edit">
                          <ActionIcon
                            variant="subtle"
                            aria-label={`Edit ${e.term}`}
                            onClick={() => setEditing(e.id)}
                          >
                            <IconPencil size={16} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Delete">
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            aria-label={`Delete ${e.term}`}
                            loading={remove.isPending && remove.variables === e.id}
                            onClick={() => remove.mutate(e.id)}
                          >
                            <IconTrash size={16} />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ),
              )}
            </Table.Tbody>
          </Table>
        </>
      )}
    </Stack>
  );
}
