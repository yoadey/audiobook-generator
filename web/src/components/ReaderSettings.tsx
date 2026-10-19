import {
  Button,
  Drawer,
  Group,
  Select,
  SegmentedControl,
  Slider,
  Stack,
  Switch,
  Text,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import type { ReactNode } from 'react';

import {
  DEFAULT_PREFS,
  type Font,
  FONTS,
  LINE_HEIGHT,
  MARGIN,
  type ReaderPrefs,
  SIZE,
  type Width,
} from '../lib/readerPrefs';

function Setting({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Stack gap={4}>
      <Text size="sm" fw={500}>
        {label}
      </Text>
      {children}
    </Stack>
  );
}

/**
 * The reading settings: the text stays visible next to (or above) them, so
 * every change shows right away.
 */
export function ReaderSettings({
  opened,
  onClose,
  prefs,
  onChange,
}: {
  opened: boolean;
  onClose: () => void;
  prefs: ReaderPrefs;
  onChange: (prefs: ReaderPrefs) => void;
}) {
  const mobile = useMediaQuery('(max-width: 36em)');
  const set = <K extends keyof ReaderPrefs>(key: K, value: ReaderPrefs[K]) =>
    onChange({ ...prefs, [key]: value });

  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      title="Reading settings"
      position={mobile ? 'bottom' : 'right'}
      size={mobile ? '60%' : 340}
      // The text stays usable beside it: scroll it, click a passage.
      withOverlay={false}
      lockScroll={false}
      closeButtonProps={{ 'aria-label': 'Close' }}
    >
      <Stack gap="md" pb="md">
        <Setting label="Page turning">
          <SegmentedControl
            size="xs"
            fullWidth
            aria-label="Page turning"
            value={prefs.turning}
            onChange={(v) => set('turning', v as ReaderPrefs['turning'])}
            data={[
              { value: 'scroll', label: 'Scroll' },
              { value: 'pages', label: 'Pages' },
            ]}
          />
        </Setting>
        <Select
          label="Font"
          value={prefs.font}
          onChange={(v) => v && set('font', v as Font)}
          allowDeselect={false}
          comboboxProps={{ withinPortal: true }}
          data={Object.entries(FONTS).map(([value, f]) => ({ value, label: f.label }))}
          renderOption={({ option }) => (
            <Text style={{ fontFamily: FONTS[option.value as Font].family }}>{option.label}</Text>
          )}
          styles={{ input: { fontFamily: FONTS[prefs.font].family } }}
        />
        <Setting label={`Text size: ${prefs.size}px`}>
          <Slider
            thumbLabel="Text size"
            min={SIZE.min}
            max={SIZE.max}
            step={1}
            value={prefs.size}
            onChange={(v) => set('size', v)}
            label={(v) => `${v}px`}
          />
        </Setting>
        <Setting label={`Line spacing: ${prefs.lineHeight.toFixed(2)}`}>
          <Slider
            thumbLabel="Line spacing"
            min={LINE_HEIGHT.min}
            max={LINE_HEIGHT.max}
            step={0.05}
            value={prefs.lineHeight}
            onChange={(v) => set('lineHeight', Math.round(v * 100) / 100)}
            label={(v) => v.toFixed(2)}
          />
        </Setting>
        <Setting label="Text width">
          <SegmentedControl
            size="xs"
            fullWidth
            aria-label="Text width"
            value={prefs.width}
            onChange={(v) => set('width', v as Width)}
            data={[
              { value: 'narrow', label: 'Narrow' },
              { value: 'medium', label: 'Medium' },
              { value: 'wide', label: 'Wide' },
              { value: 'full', label: 'Full' },
            ]}
          />
        </Setting>
        <Setting label={`Side margins: ${prefs.margin}px`}>
          <Slider
            thumbLabel="Side margins"
            min={MARGIN.min}
            max={MARGIN.max}
            step={4}
            value={prefs.margin}
            onChange={(v) => set('margin', v)}
            label={(v) => `${v}px`}
          />
        </Setting>
        <Setting label="Alignment">
          <SegmentedControl
            size="xs"
            fullWidth
            aria-label="Alignment"
            value={prefs.align}
            onChange={(v) => set('align', v as ReaderPrefs['align'])}
            data={[
              { value: 'start', label: 'Left' },
              { value: 'justify', label: 'Justified' },
            ]}
          />
        </Setting>
        <Setting label="Paragraphs">
          <SegmentedControl
            size="xs"
            fullWidth
            aria-label="Paragraphs"
            value={prefs.paragraphs}
            onChange={(v) => set('paragraphs', v as ReaderPrefs['paragraphs'])}
            data={[
              { value: 'spaced', label: 'Spaced' },
              { value: 'indented', label: 'Indented' },
            ]}
          />
        </Setting>
        <Setting label="Page colour">
          <SegmentedControl
            size="xs"
            fullWidth
            aria-label="Page colour"
            value={prefs.paper}
            onChange={(v) => set('paper', v as ReaderPrefs['paper'])}
            data={[
              { value: 'default', label: 'Default' },
              { value: 'sepia', label: 'Sepia' },
            ]}
          />
        </Setting>
        <Setting label="Mark while playing">
          <SegmentedControl
            size="xs"
            fullWidth
            aria-label="Mark while playing"
            value={prefs.highlight}
            onChange={(v) => set('highlight', v as ReaderPrefs['highlight'])}
            data={[
              { value: 'word', label: 'Sentence + word' },
              { value: 'sentence', label: 'Sentence' },
              { value: 'off', label: 'Nothing' },
            ]}
          />
        </Setting>
        <Switch
          label="Mark the saved position while paused"
          checked={prefs.showPosition}
          onChange={(e) => set('showPosition', e.currentTarget.checked)}
        />
        <Group justify="flex-end">
          <Button variant="subtle" size="xs" onClick={() => onChange(DEFAULT_PREFS)}>
            Reset to defaults
          </Button>
        </Group>
      </Stack>
    </Drawer>
  );
}
