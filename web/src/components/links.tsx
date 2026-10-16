import {
  Anchor,
  type AnchorProps,
  Button,
  type ButtonProps,
  Card,
  type CardProps,
} from '@mantine/core';
import { createLink } from '@tanstack/react-router';
import { forwardRef } from 'react';

// Router-aware Mantine components: typed `to`/`params` (Mantine's polymorphic
// `component={Link}` loses the route types).

const AnchorBase = forwardRef<HTMLAnchorElement, AnchorProps & React.ComponentPropsWithoutRef<'a'>>(
  (props, ref) => <Anchor ref={ref} {...props} />,
);
AnchorBase.displayName = 'AnchorBase';

const CardBase = forwardRef<HTMLAnchorElement, CardProps & React.ComponentPropsWithoutRef<'a'>>(
  (props, ref) => <Card ref={ref} component="a" {...props} />,
);
CardBase.displayName = 'CardBase';

const ButtonBase = forwardRef<HTMLAnchorElement, ButtonProps & React.ComponentPropsWithoutRef<'a'>>(
  (props, ref) => <Button ref={ref} component="a" {...props} />,
);
ButtonBase.displayName = 'ButtonBase';

export const AnchorLink = createLink(AnchorBase);
export const ButtonLink = createLink(ButtonBase);
export const CardLink = createLink(CardBase);
