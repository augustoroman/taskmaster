/** The pastel palette new tags get (same as the server's). */
export const PALETTE = [
  "#f8b4c0", "#fbc4a4", "#fcd89a", "#f3eaa0", "#d2eca4", "#b5e6b9",
  "#a8e0d6", "#aed6f1", "#bcc6f5", "#d3bdf2", "#efb9e6", "#e3d3bd",
];

const DARK = "#1d1d1b";
const LIGHT = "#ffffff";

function luminance(hex: string): number {
  const n = parseInt(hex.replace("#", ""), 16);
  const channel = (c: number) => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel((n >> 16) & 255) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Dark or light text, whichever contrasts more with the background. */
export function textOn(bg: string): string {
  if (!/^#[0-9a-f]{6}$/i.test(bg)) return DARK;
  return contrast(bg, DARK) >= contrast(bg, LIGHT) ? DARK : LIGHT;
}

/** Inline style for a tag pill. */
export function tagStyle(color: string) {
  return color ? { background: color, color: textOn(color), borderColor: color } : undefined;
}
