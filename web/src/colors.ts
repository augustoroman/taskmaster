/** Google Docs' "light 1" row: the colors new tags get (same as the server's). */
const LIGHT_1 = ["#cc4125", "#e06666", "#f6b26b", "#ffd966", "#93c47d", "#76a5af", "#6d9eeb", "#6fa8dc", "#8e7cc3", "#c27ba0"];
/** Google Docs' "dark 1" row: the same hues, stronger. */
const DARK_1 = ["#a61c00", "#cc0000", "#e69138", "#f1c232", "#6aa84f", "#45818e", "#3c78d8", "#3d85c6", "#674ea7", "#a64d79"];

/** The picker's swatches, in rows. */
export const PALETTE_ROWS = [LIGHT_1, DARK_1];

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
