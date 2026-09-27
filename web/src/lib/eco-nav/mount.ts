export type EcoNavSurface = "crm" | "public_form";

export function shouldMountEcoTopNav(input: { surface: EcoNavSurface; authenticated: boolean }): boolean {
  if (input.surface !== "crm") return false;
  return input.authenticated;
}
