import { DOMParser, XMLSerializer } from "@xmldom/xmldom";

export interface NfoActor {
  element: Element;
  name: string;
  hasThumb: boolean;
  hasProfile: boolean;
}

export function parseActors(xml: string): { document: Document; actors: NfoActor[] } {
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
    if (!name) continue;
    actors.push({
      element,
      name,
      hasThumb: element.getElementsByTagName("thumb").length > 0,
      hasProfile: element.getElementsByTagName("profile").length > 0,
    });
  }
  return { document, actors };
}

export function updateActor(document: Document, actor: NfoActor, thumb?: string, profile?: string): boolean {
  let changed = false;
  if (thumb && !actor.hasThumb) {
    const child = document.createElement("thumb");
    child.appendChild(document.createTextNode(thumb));
    actor.element.appendChild(child);
    changed = true;
  }
  if (profile && !actor.hasProfile) {
    const child = document.createElement("profile");
    child.appendChild(document.createTextNode(profile));
    actor.element.appendChild(child);
    changed = true;
  }
  return changed;
}

export function serializeNfo(document: Document): string {
  return new XMLSerializer().serializeToString(document);
}
