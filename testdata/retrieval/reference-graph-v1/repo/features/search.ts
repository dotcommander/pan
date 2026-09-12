export function search(registry: ServiceRegistry, cache: CacheStore, routes: RouteTable): string { return cache.read(routes.match(registry.resolve("search"))) }
