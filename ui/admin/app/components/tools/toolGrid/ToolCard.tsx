import { Trash } from "lucide-react";

import { ToolReference } from "~/lib/model/toolReferences";
import { cn, timeSince } from "~/lib/utils";

import { ConfirmationDialog } from "~/components/composed/ConfirmationDialog";
import { Truncate } from "~/components/composed/typography";
import { ToolIcon } from "~/components/tools/ToolIcon";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
    Card,
    CardContent,
    CardFooter,
    CardHeader,
} from "~/components/ui/card";
import {
    Tooltip,
    TooltipContent,
    TooltipTrigger,
} from "~/components/ui/tooltip";

interface ToolCardProps {
    tool: ToolReference;
    onDelete: (id: string) => void;
}

export function ToolCard({ tool, onDelete }: ToolCardProps) {
    return (
        <Card
            className={cn("flex flex-col h-full", {
                "border-2 border-primary": tool.metadata?.bundle,
                "border-2 border-error": tool.error,
            })}
        >
            <CardHeader className="pb-2">
                <h4 className="truncate flex flex-wrap items-center gap-x-2">
                    <div className="flex flex-nowrap gap-x-2">
                        <ToolIcon
                            className="w-5 min-w-5 h-5"
                            name={tool.name}
                            icon={tool.metadata?.icon}
                        />
                        {tool.name}
                    </div>
                    {tool.error && (
                        <Tooltip>
                            <TooltipTrigger>
                                <Badge className="bg-error mb-1 pointer-events-none">
                                    Failed
                                </Badge>
                            </TooltipTrigger>
                            <TooltipContent className="max-w-xs bg-error-foreground border border-error text-foreground">
                                <p>{tool.error}</p>
                            </TooltipContent>
                        </Tooltip>
                    )}
                    {tool.metadata?.bundle && (
                        <Badge className="pointer-events-none">Bundle</Badge>
                    )}
                </h4>
            </CardHeader>
            <CardContent className="flex-grow">
                {!tool.builtin && (
                    <Truncate className="max-w-full">{tool.reference}</Truncate>
                )}
                <p className="mt-2 text-sm text-muted-foreground line-clamp-2">
                    {tool.description || "No description available"}
                </p>
            </CardContent>
            <CardFooter className="flex justify-between items-center pt-2 h-14">
                <small className="text-muted-foreground">
                    {timeSince(new Date(tool.created))} ago
                </small>

                {!tool.builtin && (
                    <ConfirmationDialog
                        title="Delete Tool Reference"
                        description="Are you sure you want to delete this tool reference? This action cannot be undone."
                        onConfirm={() => onDelete(tool.id)}
                        confirmProps={{
                            variant: "destructive",
                            children: "Delete",
                        }}
                    >
                        <Button variant="ghost" size="icon">
                            <Trash className="w-5 h-5" />
                        </Button>
                    </ConfirmationDialog>
                )}
            </CardFooter>
        </Card>
    );
}
