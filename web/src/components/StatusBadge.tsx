import { Badge } from '@mantine/core';

import type { Book } from '../api/client';

const statusColor: Record<Book['status'], string> = {
  importing: 'blue',
  imported: 'teal',
  analyzing: 'violet',
  ready: 'green',
  failed: 'red',
};

export function StatusBadge({ status }: { status: Book['status'] }) {
  return (
    <Badge variant="light" color={statusColor[status]} style={{ flexShrink: 0 }}>
      {status}
    </Badge>
  );
}
