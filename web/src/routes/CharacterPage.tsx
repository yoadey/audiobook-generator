import {
  Alert,
  Badge,
  Breadcrumbs,
  Button,
  Card,
  Center,
  Group,
  Loader,
  MultiSelect,
  Rating,
  ScrollArea,
  Select,
  SimpleGrid,
  Stack,
  TagsInput,
  Text,
  Textarea,
  TextInput,
  Title,
} from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconArrowsSplit, IconDeviceFloppy, IconTrash } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useState } from 'react';

import {
  bookQuery,
  type CharacterDetail,
  characterQuery,
  deleteCharacter,
  splitCharacter,
  updateCharacter,
} from '../api/client';
import { AnchorLink } from '../components/links';
import { formatNumber } from '../lib/format';
import { notifyError, notifySuccess } from '../lib/notify';

const genders = [
  { value: 'female', label: 'Female' },
  { value: 'male', label: 'Male' },
  { value: 'unknown', label: 'Unknown' },
];
const ages = [
  { value: 'child', label: 'Child' },
  { value: 'teen', label: 'Teen' },
  { value: 'adult', label: 'Adult' },
  { value: 'elderly', label: 'Elderly' },
  { value: 'unknown', label: 'Unknown' },
];

function SplitForm({
  character,
  onSplit,
}: {
  character: CharacterDetail;
  onSplit: (name: string, aliases: string[]) => void;
}) {
  const [aliases, setAliases] = useState<string[]>([]);
  const [name, setName] = useState('');
  const effectiveName = name.trim() || aliases[0] || '';
  return (
    <Stack>
      <Text size="sm">
        Moves aliases that belong to someone else to a new character. Its chapters are copied from{' '}
        {character.name}; the two are not proposed for merging again.
      </Text>
      <MultiSelect
        label="Aliases to move"
        data={character.aliases}
        value={aliases}
        onChange={setAliases}
        searchable
      />
      <TextInput
        label="Name of the new character"
        placeholder={aliases[0] ?? ''}
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <Group justify="flex-end">
        <Button
          disabled={!effectiveName}
          onClick={() =>
            onSplit(
              effectiveName,
              aliases.filter((a) => a !== effectiveName),
            )
          }
        >
          Split off
        </Button>
      </Group>
    </Stack>
  );
}

/** Editable profile; remounted (key) when the stored character changes. */
function ProfileForm({ character, onSaved }: { character: CharacterDetail; onSaved: () => void }) {
  const [v, setV] = useState({
    name: character.name,
    aliases: character.aliases,
    gender: character.gender,
    ageGroup: character.ageGroup,
    role: character.role,
    description: character.description,
    speechStyle: character.speechStyle,
    importance: character.importance,
  });
  const save = useMutation({
    mutationFn: () => updateCharacter(character.id, v),
    onSuccess: () => {
      notifySuccess('Character saved');
      onSaved();
    },
    onError: (e) => notifyError('Could not save the character', e),
  });
  const dirty =
    JSON.stringify(v) !==
    JSON.stringify({
      name: character.name,
      aliases: character.aliases,
      gender: character.gender,
      ageGroup: character.ageGroup,
      role: character.role,
      description: character.description,
      speechStyle: character.speechStyle,
      importance: character.importance,
    });
  return (
    <Card withBorder>
      <Stack gap="sm">
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label="Name"
            value={v.name}
            onChange={(e) => setV({ ...v, name: e.currentTarget.value })}
          />
          <TextInput
            label="Role"
            value={v.role}
            onChange={(e) => setV({ ...v, role: e.currentTarget.value })}
          />
        </SimpleGrid>
        <TagsInput
          label="Aliases"
          description="Other names and titles the book uses for this character"
          value={v.aliases}
          onChange={(aliases) => setV({ ...v, aliases })}
          clearable
        />
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <Select
            label="Gender"
            data={genders}
            value={v.gender}
            onChange={(g) => g && setV({ ...v, gender: g as CharacterDetail['gender'] })}
            allowDeselect={false}
          />
          <Select
            label="Age"
            data={ages}
            value={v.ageGroup}
            onChange={(a) => a && setV({ ...v, ageGroup: a as CharacterDetail['ageGroup'] })}
            allowDeselect={false}
          />
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              Importance
            </Text>
            <Rating
              value={v.importance}
              onChange={(importance) => setV({ ...v, importance: Math.max(1, importance) })}
              count={5}
              aria-label="Importance"
            />
          </Stack>
        </SimpleGrid>
        <TextInput
          label="Speech style"
          description="How they talk; used to choose a voice"
          value={v.speechStyle}
          onChange={(e) => setV({ ...v, speechStyle: e.currentTarget.value })}
        />
        <Textarea
          label="Description"
          autosize
          minRows={2}
          value={v.description}
          onChange={(e) => setV({ ...v, description: e.currentTarget.value })}
        />
        <Group justify="flex-end">
          <Button
            leftSection={<IconDeviceFloppy size={16} />}
            disabled={!dirty || !v.name.trim()}
            loading={save.isPending}
            onClick={() => save.mutate()}
          >
            Save
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}

export function CharacterPage() {
  const { bookId: rawBook, characterId: raw } = useParams({
    from: '/books/$bookId/characters/$characterId',
  });
  const bookId = Number(rawBook);
  const characterId = Number(raw);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const book = useQuery(bookQuery(bookId));
  const { data: c, error, isPending } = useQuery(characterQuery(characterId));
  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ['characters'] }),
      queryClient.invalidateQueries({ queryKey: ['books', bookId] }),
    ]);

  const split = useMutation({
    mutationFn: ({ name, aliases }: { name: string; aliases: string[] }) =>
      splitCharacter(characterId, name, aliases),
    onSuccess: async (created) => {
      await invalidate();
      notifySuccess(`Created ${created.name}`);
      await navigate({
        to: '/books/$bookId/characters/$characterId',
        params: { bookId: String(bookId), characterId: String(created.id) },
      });
    },
    onError: (e) => notifyError('Could not split the character', e),
  });
  const remove = useMutation({
    mutationFn: () => deleteCharacter(characterId),
    onSuccess: async () => {
      await invalidate();
      await navigate({ to: '/books/$bookId/characters', params: { bookId: String(bookId) } });
    },
    onError: (e) => notifyError('Could not delete the character', e),
  });

  if (isPending) {
    return (
      <Center py="xl">
        <Loader />
      </Center>
    );
  }
  if (error) {
    return (
      <Alert color="red" title="Could not load the character">
        {error.message}
      </Alert>
    );
  }

  return (
    <Stack maw={860}>
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <AnchorLink to="/books/$bookId" params={{ bookId: String(bookId) }} size="sm">
          {book.data?.title ?? 'Book'}
        </AnchorLink>
        <AnchorLink to="/books/$bookId/characters" params={{ bookId: String(bookId) }} size="sm">
          Characters
        </AnchorLink>
        <Text size="sm">{c.name}</Text>
      </Breadcrumbs>
      <Group justify="space-between" align="flex-start">
        <Stack gap={4}>
          <Group gap="xs">
            <Title order={2}>{c.name}</Title>
            {c.speaks && <Badge variant="light">speaks</Badge>}
            {c.userEdited && (
              <Badge variant="outline" color="gray">
                edited
              </Badge>
            )}
          </Group>
          <Text size="sm" c="dimmed">
            {formatNumber(c.chapterCount)} chapter{c.chapterCount === 1 ? '' : 's'}
            {c.chapterCount > 0 && `, first in chapter ${c.firstChapterIdx + 1}`}
          </Text>
        </Stack>
        <Group>
          <Button
            variant="default"
            leftSection={<IconArrowsSplit size={16} />}
            disabled={c.aliases.length === 0}
            onClick={() =>
              modals.open({
                title: `Split ${c.name}`,
                children: (
                  <SplitForm
                    character={c}
                    onSplit={(name, aliases) => {
                      modals.closeAll();
                      split.mutate({ name, aliases });
                    }}
                  />
                ),
              })
            }
          >
            Split
          </Button>
          <Button
            variant="subtle"
            color="red"
            leftSection={<IconTrash size={16} />}
            onClick={() =>
              modals.openConfirmModal({
                title: `Delete ${c.name}?`,
                children: (
                  <Text size="sm">
                    For entries that are not characters. Their lines will be read by the narrator.
                  </Text>
                ),
                labels: { confirm: 'Delete', cancel: 'Cancel' },
                confirmProps: { color: 'red' },
                onConfirm: () => remove.mutate(),
              })
            }
          >
            Delete
          </Button>
        </Group>
      </Group>

      <ProfileForm key={JSON.stringify(c)} character={c} onSaved={() => void invalidate()} />

      <Card withBorder>
        <Text fw={600} mb="xs">
          Chapters
        </Text>
        {c.chapters.length === 0 ? (
          <Text size="sm" c="dimmed">
            None.
          </Text>
        ) : (
          <ScrollArea.Autosize mah={240}>
            <Group gap="xs">
              {c.chapters.map((ch) => (
                <AnchorLink
                  key={ch.id}
                  size="sm"
                  to="/books/$bookId/chapters/$chapterId"
                  params={{ bookId: String(bookId), chapterId: String(ch.id) }}
                >
                  {ch.title}
                </AnchorLink>
              ))}
            </Group>
          </ScrollArea.Autosize>
        )}
      </Card>
    </Stack>
  );
}
