import { useHealthQuery } from "../api/health.queries";

export default function Health() {
  const { data, isPending, isError, error } = useHealthQuery();

  if (isPending) {
    return <h1>Health: loading…</h1>;
  }

  if (isError) {
    return <h1>Health: error ({error.message})</h1>;
  }

  return <h1>Health: {data.status}</h1>;
}
