export interface PersonName {
  preferredName: string | null;
  givenNames: string;
  namePrefix: string | null;
  lastName: string;
}

// The name to show when only one is wanted: the chosen preferred name, else the
// first given name.
export function displayName(person: PersonName): string {
  return (
    person.preferredName ??
    (person.givenNames.split(" ")[0] || person.givenNames)
  );
}

// Full display name: display name plus surname (with prefix), e.g. "Jan de Vries".
export function fullName(person: PersonName): string {
  const prefix = person.namePrefix ? `${person.namePrefix} ` : "";
  return `${displayName(person)} ${prefix}${person.lastName}`;
}

// Avatar initials: preferred/given initial + last-name initial, e.g. "JV".
export function personInitials(person: PersonName): string {
  return `${displayName(person).charAt(0)}${person.lastName.charAt(0)}`.toUpperCase();
}

// Display name plus an abbreviated surname, e.g. "Jan de V." — fits the narrow
// sidebar where the full name would truncate.
export function shortName(person: PersonName): string {
  const prefix = person.namePrefix ? `${person.namePrefix} ` : "";
  return `${displayName(person)} ${prefix}${person.lastName.charAt(0).toUpperCase()}.`;
}
