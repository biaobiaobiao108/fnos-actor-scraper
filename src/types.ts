export interface ActorProfile {
  name: string;
  aliases: string[];
  imageUrl?: string;
  biography?: string;
  birthday?: string;
  sourceUrls: string[];
  sourceNames: string[];
}

export interface FnPerson {
  guid: string;
  trim_id?: string;
  tmdb_id?: number;
  imdb_id?: string;
  name?: string;
  original_name?: string;
  biography?: string;
  profile_path?: string;
  is_official?: boolean;
  name_locked?: boolean;
  biography_locked?: boolean;
  profile_path_locked?: boolean;
}

export interface ScrapeOptions {
  cacheDir: string;
  refresh: boolean;
}
