export interface ActorProfile {
  name: string;
  aliases: string[];
  imageUrl?: string;
  biography?: string;
  birthday?: string;
  sourceUrls: string[];
  sourceNames: string[];
}

export interface ScrapeOptions {
  cacheDir: string;
  refresh: boolean;
}
