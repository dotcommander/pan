export function support(registry: ServiceRegistry, routes: RouteTable): string { return routes.match(registry.resolve("support")) }
