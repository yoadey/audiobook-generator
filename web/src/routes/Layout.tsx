import {
  ActionIcon,
  AppShell,
  Burger,
  Group,
  NavLink,
  Title,
  useComputedColorScheme,
  useMantineColorScheme,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import {
  IconBooks,
  IconListCheck,
  IconMicrophone,
  IconMoon,
  IconServer,
  IconSettings,
  IconSun,
} from '@tabler/icons-react';
import { Link, Outlet, useRouterState } from '@tanstack/react-router';

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
          <ActionIcon
            variant="default"
            size="lg"
            aria-label="Toggle color scheme"
            onClick={() => setColorScheme(scheme === 'dark' ? 'light' : 'dark')}
          >
            {scheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
          </ActionIcon>
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
