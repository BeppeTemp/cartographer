import { templateShape } from "../components/TemplateCards";

describe("templateShape", () => {
  it("reads the fields a template adds, its brief and its sections", () => {
    const text =
      '---\ntype: api\ntitle: "{{title}}"\ntimestamp: {{timestamp}}\nstatus: draft\nprotocol: rest\nroles: [{{roles}}]\nprovenance: [{{provenance}}]\n---\n\n# {{title}}\n\n> One line: what it lets you do.\n\n## Purpose\n\ntext\n\n## Access\n\n## Links\n';
    expect(templateShape(text)).toEqual({
      fields: ["protocol", "roles"],
      brief: "One line: what it lets you do.",
      sections: ["Purpose", "Access", "Links"],
    });
  });

  it("copes with a template without frontmatter or brief", () => {
    expect(templateShape("## Only\n")).toEqual({ fields: [], brief: null, sections: ["Only"] });
  });
});
