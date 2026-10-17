import {
  Alert,
  Badge,
  Breadcrumbs,
  Button,
  Center,
  Checkbox,
  Group,
  Loader,
  Pagination,
  Radio,
  Rating,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { modals } from '@mantine/modals';
import { IconGitMerge, IconMessage, IconSearch } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { useState } from 'react';

import {
  analysisQuery,
  bookQuery,
  type Character,
  charactersQuery,
  type CharacterSort,
  mergeCharacters,
  quotesQuery,
  type Tier,
} from '../api/client';
import { AnchorLink } from '../components/links';
import { VoicePreviewButton } from '../components/VoicePreviewButton';
import { useCastingWatch } from '../lib/casting';
import { usePreview } from '../lib/preview';
import { formatNumber } from '../lib/format';
import { notifyError, notifySuccess } from '../lib/notify';

const PAGE_SIZE = 50;

const sorts: { value: CharacterSort; label: string }[] = [
  { value: 'importance', label: 'Most important' },
  { value: 'chapters', label: 'Most chapters' },
  { value: 'first', label: 'First appearance' },
  { value: 'name', label: 'Name' },
];

const genderLabel: Record<Character['gender'], string> = { female: 'f', male: 'm', unknown: '?' };

type TierFilter = 'all' | Tier;

/** The voice a character speaks with, as a short line. */
function VoiceLabel({ c }: { c: Character }) {
  return (
    <Stack gap={0}>
      <Text size="sm" ff="monospace" lineClamp={1}>
        {c.voice || '—'}
        {c.voiceSpeed !== 1 && ` · ${c.voiceSpeed.toFixed(2)}×`}
      </Text>
      <Text size="xs" c="dimmed">
        {c.voiceSource === 'own'
          ? c.voiceManual
            ? 'chosen by you'
            : 'cast'
          : c.voiceSource === 'pool'
            ? `pool voice (${genderLabel[c.gender]}, ${c.ageGroup})`
            : 'narrator (no fitting voice)'}
      </Text>
    </Stack>
  );
}

function MergeForm({
  selected,
  onMerge,
}: {
  selected: Character[];
  onMerge: (targetId: number) => void;
}) {
  // The character appearing in most chapters keeps its name by default.
  const [target, setTarget] = useState(
    String([...selected].sort((a, b) => b.chapterCount - a.chapterCount)[0]?.id ?? ''),
  );
  return (
    <Stack>
      <Text size="sm">
        The other names become aliases of the remaining character; their chapters are combined.
      </Text>
      <Radio.Group label="Keep" value={target} onChange={setTarget}>
        <Stack gap="xs" mt="xs">
          {selected.map((c) => (
            <Radio
              key={c.id}
              value={String(c.id)}
              label={`${c.name} (${formatNumber(c.chapterCount)} chapters)`}
            />
          ))}
        </Stack>
      </Radio.Group>
      <Group justify="flex-end">
        <Button onClick={() => onMerge(Number(target))}>Merge</Button>
      </Group>
    </Stack>
  );
}

export function CharactersPage() {
  const { bookId: raw } = useParams({ from: '/books/$bookId/characters/' });
  const bookId = Number(raw);
  const queryClient = useQueryClient();
  const book = useQuery(bookQuery(bookId));
  const analysis = useQuery(analysisQuery(bookId));
  const [search, setSearch] = useState('');
  const [debounced] = useDebouncedValue(search.trim(), 300);
  const [sort, setSort] = useState<CharacterSort>('importance');
  const [pageNo, setPage] = useState(1);
  const [selected, setSelected] = useState<Map<number, Character>>(new Map());
  const [tier, setTier] = useState<TierFilter>('all');
  const chars = useQuery(
    charactersQuery(bookId, debounced, sort, pageNo, PAGE_SIZE, tier === 'all' ? undefined : tier),
  );
  const preview = usePreview(bookId);
  const casting = useCastingWatch(bookId);
  // A real quote of the character if one is found, else a standard sentence.
  const sample = (id: number) => async () => {
    try {
      const qs = await queryClient.fetchQuery(quotesQuery(id));
      return qs.length > 0 ? qs[Math.floor(Math.random() * qs.length)]?.text : undefined;
    } catch {
      return undefined;
    }
  };

  const busy =
    analysis.data?.status === 'queued' ||
    analysis.data?.status === 'running' ||
    analysis.data?.status === 'merging';

  const merge = useMutation({
    mutationFn: (targetId: number) =>
      mergeCharacters(
        bookId,
        targetId,
        [...selected.keys()].filter((id) => id !== targetId),
      ),
    onSuccess: (c) => {
      notifySuccess(`Merged into ${c.name}`);
      setSelected(new Map());
      return queryClient.invalidateQueries({ queryKey: ['books', bookId] });
    },
    onError: (e) => notifyError('Could not merge characters', e),
  });

  const toggle = (c: Character, on: boolean) =>
    setSelected((prev) => {
      const next = new Map(prev);
      if (on) {
        next.set(c.id, c);
      } else {
        next.delete(c.id);
      }
      return next;
    });

  const total = chars.data?.total ?? 0;

  return (
    <Stack>
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <AnchorLink to="/books/$bookId" params={{ bookId: String(bookId) }} size="sm">
          {book.data?.title ?? 'Book'}
        </AnchorLink>
        <Text size="sm">Characters</Text>
      </Breadcrumbs>
      <Group justify="space-between">
        <Title order={2}>Characters</Title>
        <Text c="dimmed" size="sm">
          {formatNumber(total)} {debounced ? 'found' : 'in total'}
        </Text>
      </Group>
      {(casting?.status === 'queued' || casting?.status === 'running') && (
        <Alert color="blue" variant="light">
          Voices are being cast; the list updates when the casting is done.
        </Alert>
      )}
      {busy && (
        <Alert color="blue" variant="light">
          The analysis is still running: the list grows as it goes. Merging is possible once it is
          paused or done.
        </Alert>
      )}

      <Group>
        <TextInput
          placeholder="Search names and aliases"
          leftSection={<IconSearch size={14} />}
          value={search}
          onChange={(e) => {
            setSearch(e.currentTarget.value);
            setPage(1);
          }}
          w={280}
        />
        <Select
          data={sorts}
          value={sort}
          onChange={(v) => {
            if (v) {
              setSort(v as CharacterSort);
              setPage(1);
            }
          }}
          allowDeselect={false}
          w={200}
          aria-label="Sort"
        />
        <SegmentedControl
          value={tier}
          onChange={(v) => {
            setTier(v as TierFilter);
            setPage(1);
          }}
          data={[
            { value: 'all', label: 'All' },
            { value: 'main', label: 'Main' },
            { value: 'minor', label: 'Minor' },
          ]}
          aria-label="Tier"
        />
        {selected.size >= 2 && (
          <Button
            leftSection={<IconGitMerge size={16} />}
            disabled={busy}
            loading={merge.isPending}
            onClick={() =>
              modals.open({
                title: `Merge ${selected.size} characters`,
                children: (
                  <MergeForm
                    selected={[...selected.values()]}
                    onMerge={(id) => {
                      modals.closeAll();
                      merge.mutate(id);
                    }}
                  />
                ),
              })
            }
          >
            Merge {selected.size} selected
          </Button>
        )}
        {selected.size > 0 && (
          <Button variant="subtle" onClick={() => setSelected(new Map())}>
            Clear selection
          </Button>
        )}
      </Group>

      {chars.error && (
        <Alert color="red" title="Could not load characters">
          {chars.error.message}
        </Alert>
      )}
      {chars.isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {chars.data && total === 0 && (
        <Text c="dimmed" ta="center" py="xl">
          {debounced
            ? 'No character matches.'
            : 'No characters yet. Start the character analysis on the book page.'}
        </Text>
      )}
      {chars.data && total > 0 && (
        <Table.ScrollContainer minWidth={720} type="native">
          <Table striped highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th w={40} />
                <Table.Th>Name</Table.Th>
                <Table.Th>Gender · age</Table.Th>
                <Table.Th>Role</Table.Th>
                <Table.Th>Importance</Table.Th>
                <Table.Th ta="right">Chapters</Table.Th>
                <Table.Th>Voice</Table.Th>
                <Table.Th w={40} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {chars.data.items.map((c) => (
                <Table.Tr key={c.id}>
                  <Table.Td>
                    <Checkbox
                      aria-label={`Select ${c.name}`}
                      checked={selected.has(c.id)}
                      onChange={(e) => toggle(c, e.currentTarget.checked)}
                    />
                  </Table.Td>
                  <Table.Td>
                    <Group gap={6} wrap="nowrap">
                      <AnchorLink
                        to="/books/$bookId/characters/$characterId"
                        params={{ bookId: String(bookId), characterId: String(c.id) }}
                        fw={500}
                      >
                        {c.name}
                      </AnchorLink>
                      {c.speaks && (
                        <Tooltip label="Speaks">
                          <IconMessage size={14} aria-label="speaks" />
                        </Tooltip>
                      )}
                      {c.tier === 'main' && (
                        <Badge size="xs" variant="light">
                          main
                        </Badge>
                      )}
                      {c.userEdited && (
                        <Badge size="xs" variant="outline" color="gray">
                          edited
                        </Badge>
                      )}
                    </Group>
                    {c.aliases.length > 0 && (
                      <Text size="xs" c="dimmed" lineClamp={1}>
                        {c.aliases.join(', ')}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">
                      {genderLabel[c.gender]} · {c.ageGroup === 'unknown' ? '?' : c.ageGroup}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" lineClamp={2}>
                      {c.role}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Rating value={c.importance} count={5} readOnly size="xs" />
                  </Table.Td>
                  <Table.Td ta="right">{formatNumber(c.chapterCount)}</Table.Td>
                  <Table.Td>
                    <VoiceLabel c={c} />
                  </Table.Td>
                  <Table.Td>
                    <VoicePreviewButton
                      id={String(c.id)}
                      voice={c.voice}
                      speed={c.voiceSpeed}
                      text={c.previewText || sample(c.id)}
                      preview={preview}
                      label={`Listen to ${c.name}`}
                    />
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {total > PAGE_SIZE && (
        <Group justify="center">
          <Pagination total={Math.ceil(total / PAGE_SIZE)} value={pageNo} onChange={setPage} />
        </Group>
      )}
    </Stack>
  );
}
