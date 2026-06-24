import { useOrganizationsQuery } from "../api/organization.queries";

export default function Organizations(): React.JSX.Element {
  const { data, isPending, isError, error } = useOrganizationsQuery();

  if (isPending) {
    return <h1>Organizations: loading…</h1>;
  }

  if (isError) {
    return <h1>Organizations: error ({error.message})</h1>;
  }

  return (
    <section>
      <h1>Organizations</h1>
      {data.length === 0 ? (
        <p>No organizations found.</p>
      ) : (
        <ul>
          {data.map((org) => (
            <li key={org.id}>{org.name}</li>
          ))}
        </ul>
      )}
    </section>
  );
}
