import { markdownToHtml } from "mde";

const SAFE_URL = /^(https?:|mailto:)/i;

/**
 * Renders markdown (descriptions and notes) to HTML with mde's renderer,
 * then strips anything but plain formatting and safe links.
 */
export function renderMarkdown(md: string): string {
  if (!md.trim()) return "";
  const template = document.createElement("template");
  template.innerHTML = markdownToHtml(md, { images: false, videos: false });
  for (const el of Array.from(template.content.querySelectorAll("*"))) {
    for (const attr of Array.from(el.attributes)) {
      if (el.tagName === "A" && attr.name === "href" && SAFE_URL.test(attr.value.trim())) continue;
      if (attr.name === "class" || attr.name === "data-indent") continue;
      el.removeAttribute(attr.name);
    }
    if (el.tagName === "A") {
      el.setAttribute("target", "_blank");
      el.setAttribute("rel", "noopener noreferrer");
    }
    if (["SCRIPT", "STYLE", "IFRAME", "OBJECT", "EMBED", "IMG", "VIDEO"].includes(el.tagName)) el.remove();
  }
  return template.innerHTML;
}
