# Appearance

Kranz adapts to the terminal you already use. By default it reads your
terminal's background color and derives a readable palette from it, so it looks
native in both light and dark profiles without configuration.

Press `Ctrl+T` to open the live picker.

## Two places settings can live

| Scope | Where | Use it for |
| --- | --- | --- |
| Project | `ui:` in `kranz.yaml` | A look shared with everyone on the project |
| Personal | your user settings file | Your own preference across all projects |

The personal file lives at `$XDG_CONFIG_HOME/kranz/settings.yaml` on Linux and
`~/Library/Application Support/kranz/settings.yaml` on macOS. Personal settings
apply to projects that do not pin their own.

Both saves are explicit: the picker shows the exact destination path and a
summary of what will be written, and nothing is saved until you confirm.

## Project configuration

```yaml
ui:
  theme: tokyo-night
  accent: "#7AA2F7"
  background: terminal
  color_mode: auto
```

| Field | Values | Default | Meaning |
| --- | --- | --- | --- |
| `theme` | a theme name | built-in | Base palette |
| `accent` | `#RRGGBB` | theme accent | Highlight color |
| `background` | `terminal`, `theme`, `#RRGGBB` | `terminal` | Canvas source |
| `color_mode` | `auto`, `dark`, `light` | `auto` | Palette mode |

### background

- `terminal` — keep your terminal's own background and derive the rest of the
  palette from it. Transparency and background images keep working.
- `theme` — paint the theme's own canvas instead.
- `#RRGGBB` — use an exact color. Kranz derives the surrounding palette and a
  readable text set from it.

### color_mode

`auto` detects whether the terminal background is light or dark. Pin `dark` or
`light` when a terminal reports its background inaccurately.

## Themes

Dark: `kranz`, `tokyo-night`, `dracula`, `nord`, `gruvbox-dark`,
`catppuccin-mocha`, `rose-pine`, `solarized-dark`, `monokai`, `everforest`,
`one-dark`, `github-dark`, `ocean`, `forest`, `amber`.

Light: `github-light`, `solarized-light`, `cream`.

Plus `high-contrast` for maximum legibility.

## The live picker

`Ctrl+T` opens it. Changes preview immediately and are not saved until you ask.

- cycle themes and preview the whole palette in place;
- edit `accent` and the canvas color as six-digit hex, by keyboard or paste;
- cycle among the sources that actually exist — theme, project, terminal, and
  your custom colors — without losing a custom color you already entered;
- reload the saved project and personal appearance without restarting Kranz;
- apply temporarily, or save to the project or to your personal settings.

Colors you type are rendered exactly as entered. Kranz does not silently shift
a color to satisfy a contrast rule; if a combination is unreadable, you will
see that it is, and can change it.

See [controls](../reference/controls) for the key bindings inside the picker.
