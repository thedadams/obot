import { ReactNode } from "react";

import {
    FormControl,
    FormDescription,
    FormItem,
    FormLabel,
    FormMessage,
} from "~/components/ui/form";

export function BasicInputItem({
    children,
    classNames = {},
    label,
    description,
}: {
    children: ReactNode;
    classNames?: {
        wrapper?: string;
        label?: string;
        description?: string;
        control?: string;
    };
    label?: ReactNode;
    description?: ReactNode;
}) {
    return (
        <FormItem className={classNames.wrapper}>
            {label && (
                <FormLabel className={classNames.label}>{label}</FormLabel>
            )}

            <FormControl className={classNames.control}>{children}</FormControl>

            <FormMessage />

            {description && (
                <FormDescription className={classNames.description}>
                    {description}
                </FormDescription>
            )}
        </FormItem>
    );
}