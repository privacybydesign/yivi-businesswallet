export interface PersonName {
  preferredName: string | null;
  givenNames: string;
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

// Full display name: display name plus surname, e.g. "Jan de Vries". The surname
// already includes any prefix.
export function fullName(person: PersonName): string {
  return `${displayName(person)} ${person.lastName}`;
}

// Avatar initials: preferred/given initial + last-name initial, e.g. "JV".
export function personInitials(person: PersonName): string {
  return `${displayName(person).charAt(0)}${person.lastName.charAt(0)}`.toUpperCase();
}
