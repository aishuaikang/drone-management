type PinyinDictionary = Record<string, string[]>;

let dictionaryPromise: Promise<PinyinDictionary> | null = null;
let sortedKeysPromise: Promise<string[]> | null = null;

function normalizePinyin(value: string) {
  return value.toLowerCase().replace(/[^a-z]/g, "");
}

function dedupe(values: string[]) {
  return Array.from(new Set(values));
}

export function loadPinyinDictionary() {
  dictionaryPromise ??= import("../assets/ime/pinyinRimeDictionary.json").then((module) => module.default as PinyinDictionary);
  return dictionaryPromise;
}

async function loadSortedKeys() {
  sortedKeysPromise ??= loadPinyinDictionary().then((dictionary) => Object.keys(dictionary).sort());
  return sortedKeysPromise;
}

export async function getPinyinCandidates(input: string, limit = 9) {
  const normalized = normalizePinyin(input);
  if (!normalized) {
    return [];
  }

  const [dictionary, keys] = await Promise.all([loadPinyinDictionary(), loadSortedKeys()]);
  const exact = dictionary[normalized] ?? [];
  const prefix: string[] = [];

  for (const key of keys) {
    if (key <= normalized || !key.startsWith(normalized)) {
      continue;
    }
    prefix.push(...dictionary[key]);
    if (exact.length + prefix.length >= limit * 3) {
      break;
    }
  }

  return dedupe([...exact, ...prefix]).slice(0, limit);
}
