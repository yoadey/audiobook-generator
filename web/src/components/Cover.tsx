import { Center, Image } from '@mantine/core';
import { IconBook2 } from '@tabler/icons-react';
import { useState } from 'react';

import { type Book, coverUrl } from '../api/client';

/** A book's cover, or a plain stand-in when it has none. */
export function Cover({
  book,
  h = 180,
  position = 'center',
}: {
  book: Book;
  h?: number;
  /** Where a cover narrower or lower than the space sits. */
  position?: 'center' | 'left top';
}) {
  const [failed, setFailed] = useState(false);
  if (!book.hasCover || failed) {
    return (
      <Center h={h} bg="var(--mantine-color-default-hover)" style={{ borderRadius: 4 }}>
        <IconBook2 size={h / 4} stroke={1.2} />
      </Center>
    );
  }
  return (
    <Image
      src={coverUrl(book.id)}
      h={h}
      fit="contain"
      styles={{ root: { objectPosition: position } }}
      alt=""
      onError={() => setFailed(true)}
    />
  );
}
