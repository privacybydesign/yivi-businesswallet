import { useParams } from "react-router";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Avatar, Card, Tag, TopBar } from "../ui";

const FORBIDDEN_STATUS = 403;
const NOT_FOUND_STATUS = 404;

function accessMessage(error: Error): string {
  if (error instanceof ApiError && error.status === FORBIDDEN_STATUS) {
    return "You are not a member of this organization.";
  }
  if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
    return "This organization does not exist.";
  }
  return error.message;
}

export default function OrganizationDetail(): React.JSX.Element {
  const { orgSlug } = useParams();
  const slug = orgSlug ?? "";

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const members = useOrganizationMembersQuery(slug, isAdmin);

  if (org.isError) {
    return (
      <>
        <TopBar title={slug} subtitle="Organization" />
        <div className="p-8">
          <Card className="p-6">
            <p className="text-[14px] text-error">{accessMessage(org.error)}</p>
          </Card>
        </div>
      </>
    );
  }

  return (
    <>
      <TopBar
        title={org.data?.name ?? slug}
        subtitle={org.isPending ? "Loading…" : `Your role: ${org.data?.role}`}
        actions={
          org.data ? (
            <Tag tone={isAdmin ? "blue" : "default"}>{org.data.role}</Tag>
          ) : undefined
        }
      />

      <div className="flex flex-col gap-6 p-8">
        <Card className="p-6">
          <h2 className="text-[16px] font-semibold">Details</h2>
          <dl className="mt-3 grid grid-cols-[120px_1fr] gap-y-2 text-[13.5px]">
            <dt className="text-muted">Name</dt>
            <dd className="text-ink">{org.data?.name ?? "—"}</dd>
            <dt className="text-muted">Slug</dt>
            <dd className="font-mono text-ink-soft">{org.data?.slug ?? "—"}</dd>
            <dt className="text-muted">ID</dt>
            <dd className="font-mono text-[12px] text-ink-soft">
              {org.data?.id ?? "—"}
            </dd>
          </dl>
        </Card>

        {isAdmin && (
          <Card className="overflow-hidden">
            <div className="border-b border-line px-6 py-4">
              <h2 className="text-[16px] font-semibold">Members</h2>
            </div>
            {members.isError ? (
              <p className="px-6 py-4 text-[14px] text-error">
                Could not load members: {members.error.message}
              </p>
            ) : (
              <table className="w-full border-collapse text-[13.5px]">
                <tbody>
                  {members.isPending ? (
                    <tr>
                      <td className="px-6 py-3 text-ink-soft" colSpan={2}>
                        Loading…
                      </td>
                    </tr>
                  ) : (
                    members.data.map((member) => (
                      <tr key={member.userId}>
                        <td className="border-b border-line px-6 py-3">
                          <div className="flex items-center gap-2.5">
                            <Avatar
                              name={member.email.split("@")[0] ?? member.email}
                              tone="violet"
                            />
                            <span className="text-ink">{member.email}</span>
                          </div>
                        </td>
                        <td className="border-b border-line px-6 py-3 text-right">
                          <Tag
                            tone={member.role === "admin" ? "blue" : "default"}
                          >
                            {member.role}
                          </Tag>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            )}
          </Card>
        )}
      </div>
    </>
  );
}
