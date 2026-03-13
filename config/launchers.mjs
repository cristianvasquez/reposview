export const launcherConfig = {
  terminal: {
    launchers: [
      {
        command: 'ghostty',
        args: ['--working-directory={dir}', '--gtk-single-instance=false'],
        unsetEnv: ['DBUS_SESSION_BUS_ADDRESS']
      },
      {
        command: 'gnome-terminal',
        args: ['--working-directory={dir}']
      }
    ]
  },
  yazi: {
    requires: ['yazi'],
    launchers: [
      {
        command: 'ghostty',
        args: ['--working-directory={dir}', '--gtk-single-instance=false', '-e', 'yazi', '{dir}'],
        unsetEnv: ['DBUS_SESSION_BUS_ADDRESS']
      },
      {
        command: 'gnome-terminal',
        args: ['--working-directory={dir}', '--', 'yazi', '{dir}']
      }
    ]
  }
};
