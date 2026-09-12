export function billing(registry: ServiceRegistry, cache: CacheStore): string { return cache.read(registry.resolve("billing")) }
