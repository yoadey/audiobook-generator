import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';

import { castingActive, castingQuery } from '../api/client';

/**
 * Watches a book's casting and reloads its characters when a casting
 * finishes, so voices cast in the background show up.
 */
export function useCastingWatch(bookId: number) {
  const queryClient = useQueryClient();
  const casting = useQuery(castingQuery(bookId));
  const active = castingActive(casting.data?.status);
  const wasActive = useRef(active);
  useEffect(() => {
    if (wasActive.current && !active) {
      void queryClient.invalidateQueries({ queryKey: ['books', bookId, 'characters'] });
      void queryClient.invalidateQueries({ queryKey: ['characters'] });
    }
    wasActive.current = active;
  }, [active, bookId, queryClient]);
  return casting.data;
}
