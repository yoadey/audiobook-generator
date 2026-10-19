import {
  ActionIcon,
  AppShell,
  Burger,
  Group,
  NavLink,
  Text,
  Title,
  Tooltip,
  useComputedColorScheme,
  useMantineColorScheme,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import {
  IconBooks,
  IconDownload,
  IconListCheck,
  IconLogout,
  IconMicrophone,
  IconMoon,
  IconServer,
  IconSettings,
  IconSun,
} from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { Link, Outlet, useRouterState } from '@tanstack/react-router';
import { useEffect } from 'react';

import { meQuery } from '../api/client';
import { useInstall } from '../lib/install';

const navItems = [
  { to: '/library', label: 'Library', icon: IconBooks },
  { to: '/jobs', label: 'Jobs', icon: IconListCheck },
  { to: '/voices', label: 'Voices', icon: IconMicrophone },
  { to: '/settings', label: 'Settings', icon: IconSettings },
  { to: '/system', label: 'System', icon: IconServer },
] as const;

export function Layout() {
  const [opened, { toggle, close }] = useDisclosure();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { setColorScheme } = useMantineColorScheme();
  const scheme = useComputedColorScheme('light');
  const me = useQuery(meQuery).data;
  const install = useInstall();

  // The installed app's title bar takes the page's colour.
  useEffect(() => {
    const color = scheme === 'dark' ? '#242424' : '#ffffff';
    for (const m of document.querySelectorAll('meta[name="theme-color"]')) {
      m.setAttribute('content', color);
    }
  }, [scheme]);

  return (
    <AppShell
      header={{ height: 56 }}
      navbar={{ width: 220, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="sm">
            <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
            <Title order={4}>Audiobook Generator</Title>
          </Group>
          <Group gap="sm">
            {me?.authEnabled && me.name && (
              <Text size="sm" c="dimmed" visibleFrom="xs">
                {me.name}
              </Text>
            )}
            {install && (
              <Tooltip label="Install as an app">
                <ActionIcon variant="default" size="lg" aria-label="Install app" onClick={install}>
                  <IconDownload size={18} />
                </ActionIcon>
              </Tooltip>
            )}
            <ActionIcon
              variant="default"
              size="lg"
              aria-label="Toggle color scheme"
              onClick={() => setColorScheme(scheme === 'dark' ? 'light' : 'dark')}
            >
              {scheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
            </ActionIcon>
            {me?.authEnabled && (
              <Tooltip label="Sign out">
                <ActionIcon
                  component="a"
                  href="/auth/logout"
                  variant="default"
                  size="lg"
                  aria-label="Sign out"
                >
                  <IconLogout size={18} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs">
        {navItems.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            component={Link}
            to={to}
            label={label}
            leftSection={<Icon size={18} stroke={1.5} />}
            active={pathname.startsWith(to)}
            onClick={close}
          />
        ))}
      </AppShell.Navbar>

      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
  );
}
