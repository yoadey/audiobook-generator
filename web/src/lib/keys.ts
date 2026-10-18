/**
 * Whether a key press belongs to something else than the player: a form
 * field, the position slider (which handles arrows itself), or an open
 * menu, popover or drawer. A focused button of the player does not count:
 * space still plays and pauses after a click on "Forward 30".
 */
export function typing(e: KeyboardEvent): boolean {
  const t = e.target instanceof HTMLElement ? e.target : null;
  return (
    e.defaultPrevented ||
    (!!t &&
      (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) ||
        t.isContentEditable ||
        t.getAttribute('role') === 'slider' ||
        !!t.closest('[role=dialog],[role=menu],[role=listbox]')))
  );
}
