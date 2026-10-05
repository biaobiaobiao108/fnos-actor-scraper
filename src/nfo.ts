import { DOMParser } from "@xmldom/xmldom";

export interface NfoActor { name: string; }

/** Read actor names from NFO XML. This module deliberately has no write/serialize path. */
export function parseActors(xml: string): { actors: NfoActor[] } {
  const errors: string[] = [];
  const document = new DOMParser({
    errorHandler: {
      warning: (message) => errors.push(message),
      error: (message) => errors.push(message),
      fatalError: (message) => errors.push(message),
    },
  }).parseFromString(xml, "application/xml");
  if (!document.documentElement || document.documentElement.nodeName === "parsererror" || errors.length) {
    throw new Error(`NFO XML parse failed: ${errors[0] || "invalid document"}`);
  }
  const actors: NfoActor[] = [];
  for (let index = 0; index < document.getElementsByTagName("actor").length; index++) {
    const element = document.getElementsByTagName("actor").item(index)!;
    const name = element.getElementsByTagName("name").item(0)?.textContent?.trim();
    if (name) actors.push({ name });
  }
  return { actors };
}
